package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// clientOp is one operation with an operationId, flattened for the client
// generators.
type clientOp struct {
	id, method, path, summary string
	deprecated                bool
	params                    []clientParam // path parameters, in path order
	query                     []clientParam
	body                      map[string]any
	bodyRequired              bool
	response                  map[string]any // schema of the first 2xx JSON response
	hasResponse               bool           // a 2xx response is declared
	errors                    []clientError  // declared 4xx and 5xx responses, by status
}

type clientParam struct {
	name     string
	required bool
	schema   map[string]any
}

// decodeDocument round-trips d through JSON so generators walk plain maps,
// exactly what a client reading openapi.json would see.
func decodeDocument(d *Document) (map[string]any, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	return doc, nil
}

func clientOps(doc map[string]any) []*clientOp {
	var ops []*clientOp

	paths := asMap(doc["paths"])
	for _, path := range sortedKeys(paths) {
		item := asMap(paths[path])
		for _, method := range sortedKeys(item) {
			o := asMap(item[method])

			id, _ := o["operationId"].(string)
			if id == "" {
				continue
			}

			op := clientOp{id: id, method: strings.ToUpper(method), path: path}
			op.summary, _ = o["summary"].(string)
			op.deprecated, _ = o["deprecated"].(bool)

			for _, p := range asSlice(o["parameters"]) {
				pm := asMap(p)
				name, _ := pm["name"].(string)
				req, _ := pm["required"].(bool)
				cp := clientParam{name: name, required: req, schema: asMap(pm["schema"])}

				switch pm["in"] {
				case "path":
					cp.required = true
					op.params = append(op.params, cp)
				case "query":
					op.query = append(op.query, cp)
				}
			}

			if rb := asMap(o["requestBody"]); rb != nil {
				op.body = asMap(asMap(asMap(rb["content"])["application/json"])["schema"])
				op.bodyRequired, _ = rb["required"].(bool)
			}

			responses := asMap(o["responses"])
			for _, code := range sortedKeys(responses) {
				if len(code) == 3 && code[0] == '2' {
					op.hasResponse = true
					op.response = asMap(asMap(asMap(asMap(responses[code])["content"])["application/json"])["schema"])

					break
				}
			}

			op.errors = errorResponses(doc, responses)

			ops = append(ops, &op)
		}
	}

	slices.SortFunc(ops, func(a, b *clientOp) int { return strings.Compare(a.id, b.id) })

	return ops
}

// lowerFirst turns "ProductsStore" into "productsStore" and "ID" into "id".
func lowerFirst(s string) string {
	if s == strings.ToUpper(s) {
		return strings.ToLower(s)
	}

	r, n := utf8.DecodeRuneInString(s)

	return string(unicode.ToLower(r)) + s[n:]
}

// TypeScriptClient returns a dependency-free TypeScript client for the
// document in the result style: every method resolves to
// { data, error, status, headers, response }, with error narrowed by status
// from the responses the document declares; [TypeScriptClientWith] picks the
// style that throws ApiError, or adds TanStack Query helpers. The file has the
// [TypeScript] types (without Zod), <Operation>Params for path parameters and
// createClient, which has one async method per operation with an operationId
// (camelCase: "products.store" becomes productsStore). It needs fetch,
// URLSearchParams and Headers, so it runs in browsers and Node 18+.
func TypeScriptClient(d *Document) (string, error) {
	return TypeScriptClientWith(d, ClientOptions{})
}

// TypeScriptClientWith is [TypeScriptClient] with options.
func TypeScriptClientWith(d *Document, o ClientOptions) (string, error) {
	switch o.Style {
	case "", StyleResult, StyleThrow:
	default:
		return "", fmt.Errorf("openapi: unknown client style %q (result or throw)", o.Style)
	}

	if o.TanStack && o.Style == StyleThrow {
		return "", errors.New("openapi: the TanStack helpers need the result style")
	}

	types, err := TypeScript(d, false)
	if err != nil {
		return "", err
	}

	doc, err := decodeDocument(d)
	if err != nil {
		return "", err
	}

	cmd := "vukem codegen:client"
	if o.Args != "" {
		cmd += " " + o.Args
	}

	var b strings.Builder

	b.WriteString(strings.Replace(types, "vukem codegen:types", cmd, 1))
	b.WriteString("\n")

	ops := clientOps(doc)

	for _, op := range ops {
		if len(op.params) == 0 {
			continue
		}

		fields := make([]string, len(op.params))
		for i, p := range op.params {
			fields[i] = fmt.Sprintf("  %s: %s;\n", propKey(p.name), tsType(p.schema, "  "))
		}

		fmt.Fprintf(&b, "export type %sParams = {\n%s};\n\n", tsName(op.id), strings.Join(fields, ""))
	}

	if o.Style == StyleThrow {
		writeThrowClient(&b, ops)
	} else {
		b.WriteString(tsResultRuntime)
		b.WriteString("\n")

		for _, op := range ops {
			b.WriteString(tsErrorUnion(op))
		}

		writeResultClient(&b, ops)

		if o.TanStack {
			writeTanStack(&b, ops)
		}
	}

	return b.String(), checkDeclarations(b.String())
}

// writeThrowClient writes the runtime, Client and createClient of the style
// that returns the data and throws ApiError.
func writeThrowClient(b *strings.Builder, ops []*clientOp) {
	b.WriteString(tsClientRuntime)
	b.WriteString("\n/** Client has one method per API operation. */\nexport interface Client {\n")

	for i, op := range ops {
		if i > 0 {
			b.WriteString("\n")
		}

		writeTSDoc(b, op, "  ")
		fmt.Fprintf(b, "  %s(%s): Promise<%s>;\n", lowerFirst(tsName(op.id)), tsArgs(op), tsResult(op))
	}

	b.WriteString("}\n\n")
	b.WriteString("/** createClient returns a typed client; methods throw ApiError on non-2xx responses. */\n")
	b.WriteString("export function createClient(options: ClientOptions = {}): Client {\n")
	b.WriteString("  const request = requester(options);\n\n  return {\n")

	for _, op := range ops {
		arg := "args"
		if tsArgs(op) == "" {
			arg = ""
		}

		call := strconv.Quote(op.method) + ", " + strconv.Quote(op.path)
		if arg != "" {
			call += ", " + arg
		}

		fmt.Fprintf(b, "    %s: (%s) => request<%s>(%s),\n", lowerFirst(tsName(op.id)), arg, tsResult(op), call)
	}

	b.WriteString("  };\n}\n")
}

func writeTSDoc(b *strings.Builder, op *clientOp, indent string) {
	fmt.Fprintf(b, "%s/**\n%s * %s %s", indent, indent, op.method, op.path)

	if op.summary != "" && op.summary != op.id {
		fmt.Fprintf(b, ": %s", strings.ReplaceAll(op.summary, "*/", "* /"))
	}

	b.WriteString("\n")

	if op.deprecated {
		fmt.Fprintf(b, "%s * @deprecated\n", indent)
	}

	fmt.Fprintf(b, "%s */\n", indent)
}

// tsArgs renders the single argument object, or "" when the operation
// takes no input. The object (and each part) is optional unless required.
func tsArgs(op *clientOp) string {
	name := tsName(op.id)

	var fields []string

	required := false

	if len(op.params) > 0 {
		fields = append(fields, "params: "+name+"Params")
		required = true
	}

	if len(op.query) > 0 {
		opt := "?"

		for _, q := range op.query {
			if q.required {
				opt = ""
				required = true
			}
		}

		fields = append(fields, "query"+opt+": "+name+"Query")
	}

	if op.body != nil {
		opt := "?"
		if op.bodyRequired {
			opt = ""
			required = true
		}

		fields = append(fields, "body"+opt+": "+name+"Body")
	}

	if len(fields) == 0 {
		return ""
	}

	arg := "args: { " + strings.Join(fields, "; ") + " }"
	if !required {
		arg = "args?: { " + strings.Join(fields, "; ") + " }"
	}

	return arg
}

func tsResult(op *clientOp) string {
	if op.hasResponse {
		return tsName(op.id) + "Response"
	}

	return "unknown"
}

// tsClientRuntime sticks to fetch, Headers, URLSearchParams and ES2017
// library calls: no dependencies, browsers and Node 18+.
const tsClientRuntime = `/** ApiProblem is an RFC 9457 problem details document; errors maps each invalid field to its messages, issues lists each problem with its code, message key and params, code is the machine-readable code set with errs.HTTPError.WithExtension, and other extension members are typed unknown. */
export type ApiProblem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
  request_id?: string;
  code?: string;
  errors?: Record<string, string[]>;
  issues?: { path: string; code: string; key?: string; message: string; params?: Record<string, unknown>; on?: string }[];
  [extension: string]: unknown;
};

/** ApiError is thrown for every non-2xx response. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ApiProblem | undefined;

  constructor(status: number, problem: ApiProblem | undefined) {
    super(problem?.detail || problem?.title || "HTTP " + status);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }

  /** errors maps each invalid field to its messages (422); empty otherwise. */
  get errors(): Record<string, string[]> {
    return this.problem?.errors ?? {};
  }
}

export type ClientOptions = {
  /** baseUrl prefixes every path; "" (the default) keeps requests on the page's origin. */
  baseUrl?: string;
  /** fetch replaces globalThis.fetch (tests, SSR, retries). */
  fetch?: typeof fetch;
  /** headers are sent with every request; a function is called per request (tokens). */
  headers?: HeadersInit | (() => HeadersInit | Promise<HeadersInit>);
  /** credentials defaults to "same-origin" (cookies only for the same origin). */
  credentials?: RequestCredentials;
};

type RequestArgs = { params?: object; query?: object; body?: unknown };

function buildPath(template: string, params: object | undefined): string {
  const values = (params ?? {}) as Record<string, unknown>;
  return template.replace(/\{([^}]+)\}/g, (_match: string, key: string): string => {
    const value = values[key];
    if (value === undefined || value === null) {
      throw new Error("api: missing path param " + key);
    }
    return encodeURIComponent(String(value));
  });
}

function buildQuery(query: object | undefined): string {
  if (!query) {
    return "";
  }
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    const items: unknown[] = Array.isArray(value) ? value : [value];
    for (const item of items) {
      if (item !== undefined && item !== null) {
        search.append(key, String(item));
      }
    }
  }
  const encoded = search.toString();
  return encoded ? "?" + encoded : "";
}

async function readBody(res: Response): Promise<unknown> {
  const text = res.status === 204 ? "" : await res.text();
  if (!text) {
    return undefined;
  }
  return /json/i.test(res.headers.get("Content-Type") ?? "") ? JSON.parse(text) : text;
}

function requester(options: ClientOptions) {
  const baseUrl = (options.baseUrl ?? "").replace(/\/+$/, "");
  const doFetch = options.fetch ?? ((input: RequestInfo | URL, init?: RequestInit) => globalThis.fetch(input, init));

  return async function request<T>(method: string, path: string, args: RequestArgs = {}): Promise<T> {
    const extra = typeof options.headers === "function" ? await options.headers() : options.headers;
    const headers = new Headers(extra);
    if (!headers.has("Accept")) {
      headers.set("Accept", "application/json");
    }
    let body: string | undefined;
    if (args.body !== undefined) {
      body = JSON.stringify(args.body);
      headers.set("Content-Type", "application/json");
    }
    const res = await doFetch(baseUrl + buildPath(path, args.params) + buildQuery(args.query), {
      method,
      headers,
      body,
      credentials: options.credentials ?? "same-origin",
    });
    let data: unknown;
    try {
      data = await readBody(res);
    } catch (err) {
      if (res.ok) {
        throw err;
      }
    }
    if (!res.ok) {
      const problem = typeof data === "object" && data !== null ? (data as ApiProblem) : undefined;
      throw new ApiError(res.status, problem);
    }
    return data as T;
  };
}
`

// ClientStyle is how a generated TypeScript client reports HTTP errors.
type ClientStyle string

const (
	// StyleResult (the default) resolves every call to { data, error, status,
	// headers, response }: an HTTP error is a value, narrowed by status.
	StyleResult ClientStyle = "result"
	// StyleThrow resolves to the data and throws ApiError on a non-2xx
	// response.
	StyleThrow ClientStyle = "throw"
)

// ClientOptions tunes [TypeScriptClientWith].
type ClientOptions struct {
	// Style is StyleResult when empty.
	Style ClientStyle
	// TanStack adds createQueries, option objects for TanStack Query; it
	// needs StyleResult and imports nothing.
	TanStack bool
	// Args is the text of the non-default flags the generator was run with;
	// it is written into the header so "vukem codegen" can run it again.
	Args string
}

// clientError is one declared 4xx or 5xx response of an operation.
type clientError struct {
	status int
	schema map[string]any // schema of the problem body; nil when it has none
}

// errorResponses collects the 4xx and 5xx responses an operation declares,
// resolving components/responses references. The range keys ("4XX") and
// "default" are not statuses: they are the generic member of the union.
func errorResponses(doc map[string]any, responses map[string]any) []clientError {
	var out []clientError

	for _, code := range sortedKeys(responses) {
		status, err := strconv.Atoi(code)
		if err != nil || len(code) != 3 || (code[0] != '4' && code[0] != '5') {
			continue
		}

		resp := asMap(responses[code])
		if ref, ok := resp["$ref"].(string); ok {
			resp = asMap(
				asMap(asMap(doc["components"])["responses"])[strings.TrimPrefix(ref, "#/components/responses/")],
			)
		}

		content := asMap(resp["content"])

		schema := asMap(asMap(content["application/problem+json"])["schema"])
		if schema == nil {
			schema = asMap(asMap(content["application/json"])["schema"])
		}

		out = append(out, clientError{status: status, schema: schema})
	}

	return out
}

// tsErrorUnion renders <Op>Error: one member per declared error status, with
// the type of its body, then a member for every other status. That member
// names the statuses left (Exclude) instead of saying number, or comparing
// error.status with 422 would not narrow the declared member.
func tsErrorUnion(op *clientOp) string {
	var b strings.Builder

	declared := make([]string, 0, len(op.errors))

	for _, e := range op.errors {
		value := "undefined"
		if e.schema != nil {
			value = tsType(e.schema, "  ")
		}

		fmt.Fprintf(&b, "\n  | { status: %d; value: %s }", e.status, value)

		declared = append(declared, strconv.Itoa(e.status))
	}

	status := "HttpErrorStatus"
	if len(declared) > 0 {
		status = "Exclude<HttpErrorStatus, " + strings.Join(declared, " | ") + ">"
	}

	fmt.Fprintf(&b, "\n  | { status: %s; value: ApiProblem | undefined }", status)

	return "export type " + tsName(op.id) + "Error =" + b.String() + ";\n\n"
}

// tsCallArgs is the parameter list of a client method: the argument object
// when the operation takes input, then the per-call options.
func tsCallArgs(op *clientOp) string {
	if args := tsArgs(op); args != "" {
		return args + ", options?: CallOptions"
	}

	return "options?: CallOptions"
}

// writeResultClient writes the Client interface and createClient of the
// result style.
func writeResultClient(b *strings.Builder, ops []*clientOp) {
	b.WriteString("\n/** Client has one method per API operation. */\nexport interface Client {\n")

	for i, op := range ops {
		if i > 0 {
			b.WriteString("\n")
		}

		writeTSDoc(b, op, "  ")
		fmt.Fprintf(b, "  %s(\n    %s,\n  ): Promise<ApiResult<%s, %sError>>;\n",
			lowerFirst(tsName(op.id)), strings.ReplaceAll(tsCallArgs(op), ", options?", ",\n    options?"),
			tsResult(op), tsName(op.id))
	}

	b.WriteString("}\n\n")
	b.WriteString(
		"/** createClient returns a typed client; methods resolve to { data, error, status, headers, response }. */\n",
	)
	b.WriteString("export function createClient(options: ClientOptions = {}): Client {\n")
	b.WriteString("  const request = requester(options);\n\n  return {\n")

	for _, op := range ops {
		params, args := "options", "undefined"
		if tsArgs(op) != "" {
			params, args = "args, options", "args"
		}

		fmt.Fprintf(b, "    %s: (%s) =>\n      request<%s, %sError>(%s, %s, %s, options),\n",
			lowerFirst(tsName(op.id)), params, tsResult(op), tsName(op.id),
			strconv.Quote(op.method), strconv.Quote(op.path), args)
	}

	b.WriteString("  };\n}\n")
}

// writeTanStack writes createQueries: for each operation the option object
// TanStack Query takes, a queryKey and queryFn for GET and HEAD, a
// mutationKey and mutationFn for the others. They are plain objects, so the
// file imports nothing.
func writeTanStack(b *strings.Builder, ops []*clientOp) {
	b.WriteString(tsUnwrap)
	b.WriteString(
		"\n/** createQueries returns option objects for TanStack Query: queryKey and queryFn for GET and HEAD, mutationKey and mutationFn for the rest. queryFn and mutationFn throw ApiError on an HTTP error. */\n",
	)
	b.WriteString("export function createQueries(client: Client) {\n  return {\n")

	for _, op := range ops {
		name := lowerFirst(tsName(op.id))
		hasArgs := tsArgs(op) != ""
		call := "{ signal }"
		key := "[" + strconv.Quote(name) + "] as const"

		if hasArgs {
			call = "args, { signal }"
			key = "[" + strconv.Quote(name) + ", args ?? {}] as const"
		}

		if op.method == "GET" || op.method == "HEAD" {
			if hasArgs && !strings.Contains(tsArgs(op), "args?") {
				key = "[" + strconv.Quote(name) + ", args] as const"
			}

			fmt.Fprintf(b, "    %s: (%s) => ({\n      queryKey: %s,\n"+
				"      queryFn: async ({ signal }: { signal?: AbortSignal }) =>\n        unwrap(await client.%s(%s)),\n    }),\n",
				name, tsArgs(op), key, name, call)

			continue
		}

		fn, call := "async () =>", "client."+name+"()"
		if hasArgs {
			fn, call = "async ("+tsArgs(op)+") =>", "client."+name+"(args)"
		}

		fmt.Fprintf(
			b,
			"    %s: () => ({\n      mutationKey: [%s] as const,\n      mutationFn: %s\n        unwrap(await %s),\n    }),\n",
			name,
			strconv.Quote(name),
			fn,
			call,
		)
	}

	b.WriteString("  };\n}\n")
}

// tsUnwrap turns a result into its data, or throws the error as ApiError,
// which is what TanStack Query expects from a query or mutation function.
const tsUnwrap = `
function unwrap<T, E extends { status: number; value: unknown }>(result: ApiResult<T, E>): T {
  if (result.error !== null) {
    const value = result.error.value;
    throw new ApiError(result.error.status, typeof value === "object" && value !== null ? (value as ApiProblem) : undefined);
  }
  // error is null exactly when data is T; a generic union cannot say so.
  return result.data as T;
}
`

// tsResultRuntime is the runtime of the result style. Like the throw style it
// needs only fetch, Headers and URLSearchParams.
const tsResultRuntime = `/** ApiProblem is an RFC 9457 problem details document; errors maps each invalid field to its messages, issues lists each problem with its code, message key and params, code is the machine-readable code set with errs.HTTPError.WithExtension, and other extension members are typed unknown. */
export type ApiProblem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
  request_id?: string;
  code?: string;
  errors?: Record<string, string[]>;
  issues?: { path: string; code: string; key?: string; message: string; params?: Record<string, unknown>; on?: string }[];
  [extension: string]: unknown;
};

/** ApiError is thrown with throwHttpError, and by the TanStack helpers, for a non-2xx response. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ApiProblem | undefined;

  constructor(status: number, problem: ApiProblem | undefined) {
    super(problem?.detail || problem?.title || "HTTP " + status);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }

  /** errors maps each invalid field to its messages (422); empty otherwise. */
  get errors(): Record<string, string[]> {
    return this.problem?.errors ?? {};
  }
}

/** HttpErrorStatus lists the error statuses the client narrows on. A status outside it is returned as it is: the type is an approximation. */
export type HttpErrorStatus =
  | 400 | 401 | 402 | 403 | 404 | 405 | 406 | 408 | 409 | 410 | 412 | 413 | 415
  | 422 | 423 | 425 | 428 | 429 | 431 | 451 | 500 | 501 | 502 | 503 | 504;

/** ApiResult is what every client method resolves to: data on 2xx, error otherwise. A failed fetch (network, abort) is not a result: it rejects. */
export type ApiResult<T, E> =
  | { data: T; error: null; status: number; headers: Headers; response: Response }
  | { data: null; error: E; status: number; headers: Headers; response: Response };

export type CallOptions = {
  /** signal aborts the request (AbortController). */
  signal?: AbortSignal;
  /** headers are added to this request only, over the client's. */
  headers?: HeadersInit;
};

export type ClientOptions = {
  /** baseUrl prefixes every path; "" (the default) keeps requests on the page's origin. */
  baseUrl?: string;
  /** fetch replaces globalThis.fetch (tests, SSR, retries). */
  fetch?: typeof fetch;
  /** headers are sent with every request; a function is called per request (tokens). */
  headers?: HeadersInit | (() => HeadersInit | Promise<HeadersInit>);
  /** credentials defaults to "same-origin" (cookies only for the same origin). */
  credentials?: RequestCredentials;
  /** onRequest runs before the request is sent; a RequestInit it returns is merged, member by member, over the one built (a returned headers replaces the whole headers). */
  onRequest?: (path: string, init: RequestInit) => RequestInit | void | Promise<RequestInit | void>;
  /** onResponse runs after every response, before the body is read. */
  onResponse?: (response: Response) => void | Promise<void>;
  /** throwHttpError makes methods throw ApiError on a non-2xx response instead of returning error. */
  throwHttpError?: boolean;
};

type RequestArgs = { params?: object; query?: object; body?: unknown };

function buildPath(template: string, params: object | undefined): string {
  const values = (params ?? {}) as Record<string, unknown>;
  return template.replace(/\{([^}]+)\}/g, (_match: string, key: string): string => {
    const value = values[key];
    if (value === undefined || value === null) {
      throw new Error("api: missing path param " + key);
    }
    return encodeURIComponent(String(value));
  });
}

function buildQuery(query: object | undefined): string {
  if (!query) {
    return "";
  }
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    const items: unknown[] = Array.isArray(value) ? value : [value];
    for (const item of items) {
      if (item !== undefined && item !== null) {
        search.append(key, String(item));
      }
    }
  }
  const encoded = search.toString();
  return encoded ? "?" + encoded : "";
}

async function readBody(res: Response): Promise<unknown> {
  const text = res.status === 204 ? "" : await res.text();
  if (!text) {
    return undefined;
  }
  return /json/i.test(res.headers.get("Content-Type") ?? "") ? JSON.parse(text) : text;
}

function requester(options: ClientOptions) {
  const baseUrl = (options.baseUrl ?? "").replace(/\/+$/, "");
  const doFetch = options.fetch ?? ((input: RequestInfo | URL, init?: RequestInit) => globalThis.fetch(input, init));

  return async function request<T, E>(
    method: string,
    path: string,
    args: RequestArgs = {},
    call: CallOptions = {},
  ): Promise<ApiResult<T, E>> {
    const extra = typeof options.headers === "function" ? await options.headers() : options.headers;
    const headers = new Headers(extra);
    new Headers(call.headers).forEach((value: string, key: string) => headers.set(key, value));
    if (!headers.has("Accept")) {
      headers.set("Accept", "application/json");
    }
    let body: string | undefined;
    if (args.body !== undefined) {
      body = JSON.stringify(args.body);
      headers.set("Content-Type", "application/json");
    }
    const url = baseUrl + buildPath(path, args.params) + buildQuery(args.query);
    let init: RequestInit = {
      method,
      headers,
      body,
      credentials: options.credentials ?? "same-origin",
      signal: call.signal,
    };
    const patch = options.onRequest ? await options.onRequest(url, init) : undefined;
    if (patch) {
      init = { ...init, ...patch };
    }
    const res = await doFetch(url, init);
    if (options.onResponse) {
      await options.onResponse(res);
    }
    let data: unknown;
    try {
      data = await readBody(res);
    } catch (err) {
      if (res.ok) {
        throw err;
      }
    }
    const meta = { status: res.status, headers: res.headers, response: res };
    if (res.ok) {
      return { data: data as T, error: null, ...meta };
    }
    const problem = typeof data === "object" && data !== null ? (data as ApiProblem) : undefined;
    if (options.throwHttpError) {
      throw new ApiError(res.status, problem);
    }
    return { data: null, error: { status: res.status, value: problem } as unknown as E, ...meta };
  };
}
`
