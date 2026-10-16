package openapi

import (
	"encoding/json"
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
// document: the [TypeScript] types (without Zod), <Operation>Params for
// path parameters, ApiError and createClient, which has one async method
// per operation with an operationId (camelCase: "products.store" becomes
// productsStore). It needs fetch, URLSearchParams and Headers, so it runs
// in browsers and Node 18+.
func TypeScriptClient(d *Document) (string, error) {
	types, err := TypeScript(d, false)
	if err != nil {
		return "", err
	}

	doc, err := decodeDocument(d)
	if err != nil {
		return "", err
	}

	var b strings.Builder

	b.WriteString(strings.Replace(types, "vukem codegen:types", "vukem codegen:client", 1))
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

	b.WriteString(tsClientRuntime)
	b.WriteString("\n/** Client has one method per API operation. */\nexport interface Client {\n")

	for i, op := range ops {
		if i > 0 {
			b.WriteString("\n")
		}

		writeTSDoc(&b, op, "  ")
		fmt.Fprintf(&b, "  %s(%s): Promise<%s>;\n", lowerFirst(tsName(op.id)), tsArgs(op), tsResult(op))
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

		fmt.Fprintf(&b, "    %s: (%s) => request<%s>(%s),\n", lowerFirst(tsName(op.id)), arg, tsResult(op), call)
	}

	b.WriteString("  };\n}\n")

	return b.String(), nil
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
const tsClientRuntime = `/** ApiProblem is an RFC 9457 problem details document; errors maps each invalid field to its messages. */
export type ApiProblem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
  request_id?: string;
  errors?: Record<string, string[]>;
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
