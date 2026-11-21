import type { Token } from "../editor/tokenizer";

/**
 * A strict JSON reader for the MongoDB console. Extended JSON is JSON (its
 * extra types are objects such as {"$oid": "..."}), so this checks the syntax
 * and reports where it broke, in the same way on every browser, before
 * anything is sent. The server still parses and checks the text itself.
 */

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export interface JsonError {
  message: string;
  /** Zero-based offset into the text. */
  offset: number;
  line: number;
  column: number;
}

export type JsonResult = { ok: true; value: Json } | { ok: false; error: JsonError };

const maxDepth = 64;

class Failure extends Error {
  constructor(
    message: string,
    readonly offset: number,
  ) {
    super(message);
  }
}

function describe(text: string, offset: number, message: string): JsonError {
  let line = 1;
  let column = 1;
  for (let i = 0; i < Math.min(offset, text.length); i++) {
    if (text[i] === "\n") {
      line++;
      column = 1;
    } else {
      column++;
    }
  }
  return { message, offset, line, column };
}

class Reader {
  private i = 0;

  constructor(private readonly s: string) {}

  read(): Json {
    this.space();
    const v = this.value(0);
    this.space();
    if (this.i < this.s.length) {
      throw new Failure("Unexpected text after the end of the value", this.i);
    }
    return v;
  }

  private space(): void {
    while (this.i < this.s.length && " \t\r\n".includes(this.s[this.i] as string)) {
      this.i++;
    }
  }

  private value(depth: number): Json {
    if (depth > maxDepth) {
      throw new Failure(`Nested more than ${maxDepth} levels deep`, this.i);
    }
    const c = this.s[this.i];
    if (c === undefined) {
      throw new Failure("The text ends where a value was expected", this.i);
    }
    if (c === "{") return this.object(depth);
    if (c === "[") return this.array(depth);
    if (c === '"') return this.string();
    if (c === "-" || (c >= "0" && c <= "9")) return this.number();
    for (const word of ["true", "false", "null"] as const) {
      if (this.s.startsWith(word, this.i)) {
        this.i += word.length;
        return word === "null" ? null : word === "true";
      }
    }
    throw new Failure(`Unexpected "${c}": a value was expected`, this.i);
  }

  private object(depth: number): Json {
    const out: { [key: string]: Json } = {};
    this.i++;
    this.space();
    if (this.s[this.i] === "}") {
      this.i++;
      return out;
    }
    for (;;) {
      this.space();
      if (this.s[this.i] !== '"') {
        throw new Failure("A field name in double quotes was expected", this.i);
      }
      const key = this.string();
      this.space();
      if (this.s[this.i] !== ":") {
        throw new Failure('A ":" was expected after the field name', this.i);
      }
      this.i++;
      this.space();
      Object.defineProperty(out, key, { value: this.value(depth + 1), enumerable: true, writable: true, configurable: true });
      this.space();
      const c = this.s[this.i];
      if (c === ",") {
        this.i++;
        continue;
      }
      if (c === "}") {
        this.i++;
        return out;
      }
      throw new Failure('A "," or a "}" was expected', this.i);
    }
  }

  private array(depth: number): Json {
    const out: Json[] = [];
    this.i++;
    this.space();
    if (this.s[this.i] === "]") {
      this.i++;
      return out;
    }
    for (;;) {
      this.space();
      out.push(this.value(depth + 1));
      this.space();
      const c = this.s[this.i];
      if (c === ",") {
        this.i++;
        continue;
      }
      if (c === "]") {
        this.i++;
        return out;
      }
      throw new Failure('A "," or a "]" was expected', this.i);
    }
  }

  private string(): string {
    const start = this.i;
    this.i++;
    let out = "";
    for (;;) {
      const c = this.s[this.i];
      if (c === undefined || c === "\n") {
        throw new Failure("This string is not closed", start);
      }
      if (c === '"') {
        this.i++;
        return out;
      }
      if (c < " ") {
        throw new Failure("A control character must be escaped inside a string", this.i);
      }
      if (c === "\\") {
        const e = this.s[this.i + 1];
        const simple: Record<string, string> = { '"': '"', "\\": "\\", "/": "/", b: "\b", f: "\f", n: "\n", r: "\r", t: "\t" };
        if (e !== undefined && e in simple) {
          out += simple[e];
          this.i += 2;
        } else if (e === "u" && /^[0-9a-fA-F]{4}$/.test(this.s.slice(this.i + 2, this.i + 6))) {
          out += String.fromCharCode(parseInt(this.s.slice(this.i + 2, this.i + 6), 16));
          this.i += 6;
        } else {
          throw new Failure("This escape is not valid in JSON", this.i);
        }
      } else {
        out += c;
        this.i++;
      }
    }
  }

  private number(): number {
    const m = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?/.exec(this.s.slice(this.i));
    if (!m) {
      throw new Failure("This is not a valid number", this.i);
    }
    this.i += m[0].length;
    return Number(m[0]);
  }
}

/** parseJSON reads one JSON value, or says where and why the text is not JSON. */
export function parseJSON(text: string): JsonResult {
  try {
    return { ok: true, value: new Reader(text).read() };
  } catch (err) {
    if (err instanceof Failure) {
      return { ok: false, error: describe(text, err.offset, err.message) };
    }
    throw err;
  }
}

/** Operators that run JavaScript on the server, which Studio never allows. */
export const scriptOperators: readonly string[] = ["$where", "$function", "$accumulator"];
/** Stages that write; they need --write. */
export const writeStages: readonly string[] = ["$out", "$merge"];

/** findKey walks a value for an object key in names and returns the first one found. */
export function findKey(value: Json, names: readonly string[]): string | null {
  if (Array.isArray(value)) {
    for (const v of value) {
      const hit = findKey(v, names);
      if (hit) return hit;
    }
  } else if (value !== null && typeof value === "object") {
    for (const [k, v] of Object.entries(value)) {
      if (names.includes(k)) return k;
      const hit = findKey(v, names);
      if (hit) return hit;
    }
  }
  return null;
}

export type ConsoleRequest =
  | { kind: "find"; collection: string; filter: string }
  | { kind: "aggregate"; collection: string; pipeline: string };

export type ConsoleCheck = { ok: true; request: ConsoleRequest } | { ok: false; message: string; line?: number; column?: number };

/**
 * checkConsole validates what the console would send: one JSON object with a
 * "collection" and either a "filter" object (find) or a "pipeline" array
 * (aggregate). It also says no early to the operators the server refuses; the
 * server does not depend on this check.
 */
export function checkConsole(text: string, write: boolean): ConsoleCheck {
  const parsed = parseJSON(text);
  if (!parsed.ok) {
    const { message, line, column } = parsed.error;
    return { ok: false, message: `Not valid JSON: ${message} (line ${line}, column ${column}).`, line, column };
  }
  const v = parsed.value;
  if (v === null || typeof v !== "object" || Array.isArray(v)) {
    return { ok: false, message: 'Write one JSON object with a "collection" and a "filter" or a "pipeline".' };
  }
  const { collection, filter, pipeline, ...extra } = v as { [key: string]: Json };
  if (typeof collection !== "string" || collection === "") {
    return { ok: false, message: 'Give the collection as a string: "collection": "users".' };
  }
  const unknown = Object.keys(extra);
  if (unknown.length > 0) {
    return { ok: false, message: `Unknown field "${unknown[0]}". Use "collection" and a "filter" or a "pipeline".` };
  }
  if ((filter === undefined) === (pipeline === undefined)) {
    return { ok: false, message: 'Give exactly one of "filter" (find) or "pipeline" (aggregate).' };
  }
  const body = (filter ?? pipeline) as Json;
  const script = findKey(body, scriptOperators);
  if (script) {
    return { ok: false, message: `${script} runs JavaScript on the server and Studio refuses it.` };
  }
  if (filter !== undefined) {
    if (filter === null || typeof filter !== "object" || Array.isArray(filter)) {
      return { ok: false, message: '"filter" must be an object.' };
    }
    return { ok: true, request: { kind: "find", collection, filter: JSON.stringify(filter) } };
  }
  if (!Array.isArray(pipeline) || pipeline.some((s) => s === null || typeof s !== "object" || Array.isArray(s))) {
    return { ok: false, message: '"pipeline" must be an array of stage objects.' };
  }
  const writing = findKey(pipeline, writeStages);
  if (writing && !write) {
    return { ok: false, message: `${writing} writes data and needs Studio to run with --write.` };
  }
  return { ok: true, request: { kind: "aggregate", collection, pipeline: JSON.stringify(pipeline) } };
}

/** The statement a new console tab starts with. */
export function template(collection: string): string {
  return `{\n  "collection": ${JSON.stringify(collection)},\n  "filter": {}\n}`;
}

/** jsonTokens splits one line into the token kinds the editor already colors; a string never spans lines. */
export function jsonTokens(line: string): Token[] {
  const out: Token[] = [];
  let i = 0;
  const push = (kind: Token["kind"], text: string): void => {
    out.push({ kind, text, start: i });
    i += text.length;
  };
  while (i < line.length) {
    const c = line[i] as string;
    if (c === " " || c === "\t") {
      let j = i;
      while (j < line.length && (line[j] === " " || line[j] === "\t")) j++;
      push("space", line.slice(i, j));
    } else if (c === '"') {
      let j = i + 1;
      while (j < line.length && line[j] !== '"') j += line[j] === "\\" ? 2 : 1;
      const end = Math.min(j + 1, line.length);
      const text = line.slice(i, end);
      const isKey = /^\s*:/.test(line.slice(end));
      push(isKey ? "identifier" : "string", text);
    } else if (/[-0-9]/.test(c)) {
      const m = /^-?[0-9][0-9.eE+-]*/.exec(line.slice(i));
      push("number", m ? m[0] : c);
    } else if (/[A-Za-z]/.test(c)) {
      const m = /^[A-Za-z_]+/.exec(line.slice(i)) as RegExpExecArray;
      push(["true", "false", "null"].includes(m[0]) ? "keyword" : "identifier", m[0]);
    } else {
      push("operator", c);
    }
  }
  return out;
}

/** JsonLineCache has the interface the editor expects of a cache of tokenized lines; JSON lines do not depend on each other. */
export class JsonLineCache {
  private lines: { text: string; tokens: Token[] }[] = [];

  update(text: string): readonly (readonly Token[])[] {
    const next = text.split("\n");
    const old = this.lines;
    this.lines = next.map((line, i) => (old[i]?.text === line ? (old[i] as { text: string; tokens: Token[] }) : { text: line, tokens: jsonTokens(line) }));
    return this.lines.map((l) => l.tokens);
  }
}
