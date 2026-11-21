/**
 * A small SQL tokenizer for the editor's highlight layer, completion and
 * formatter. It is lexical only: it never builds a tree and never fails, so
 * half-typed SQL is still drawn. The text of every token is kept, so joining
 * the tokens of a text gives the text back.
 */

export type TokenKind = "keyword" | "identifier" | "string" | "number" | "comment" | "operator" | "space";

export interface Token {
  kind: TokenKind;
  text: string;
  /** Offset of the first character, in the line for scanLine and in the text for tokenize. */
  start: number;
  /** True when the token goes on from the previous line (a string or a comment that was open). */
  continued?: boolean;
}

/** What is open at the end of a line: nothing, a block comment, a quoted string or a dollar-quoted string. */
export type LineState =
  | { open: "none" }
  | { open: "block" }
  | { open: "quote"; quote: "'" | '"' | "`" }
  | { open: "dollar"; tag: string };

export const startState: LineState = { open: "none" };

export function sameState(a: LineState, b: LineState): boolean {
  if (a.open !== b.open) {
    return false;
  }
  if (a.open === "quote" && b.open === "quote") {
    return a.quote === b.quote;
  }
  if (a.open === "dollar" && b.open === "dollar") {
    return a.tag === b.tag;
  }
  return true;
}

const keywordList = `
add all alter analyze and any as asc begin between by case cast check collate column commit constraint create cross
database default delete desc describe distinct drop else end escape except exists explain false fetch filter first
following for foreign from full group having if ilike in index inner insert intersect into is join key last lateral
left like limit materialized natural not nothing null nulls offset on only or order outer over partition pragma
preceding primary range recursive references replace returning right rollback row rows select set show table
temporary then to transaction true truncate union unique update using values view when where window with without
`;

export const keywords: ReadonlySet<string> = new Set(keywordList.split(/\s+/).filter((w) => w !== ""));

export function isKeyword(word: string): boolean {
  return keywords.has(word.toLowerCase());
}

const symbolChars = "+-*/%=<>!|&^~:@#?";

function isWordStart(c: string): boolean {
  return c === "_" || (c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || c > "\u007f";
}

function isDigit(c: string): boolean {
  return c >= "0" && c <= "9";
}

function isWordPart(c: string): boolean {
  return isWordStart(c) || isDigit(c) || c === "$";
}

function isSpace(c: string): boolean {
  return c === " " || c === "\t" || c === "\r" || c === "\f" || c === " ";
}

/** The index after the quote that closes a quoted text whose body starts at from, or -1 when the line ends first. A doubled quote is part of the text. */
function closeQuote(line: string, from: number, quote: string): number {
  for (let j = from; j < line.length; j++) {
    if (line[j] === quote) {
      if (line[j + 1] === quote) {
        j++;
        continue;
      }
      return j + 1;
    }
  }
  return -1;
}

function numberEnd(line: string, i: number): number {
  let j = i;
  if (line[j] === "0" && (line[j + 1] === "x" || line[j + 1] === "X") && /[0-9a-fA-F]/.test(line[j + 2] ?? "")) {
    j += 2;
    while (j < line.length && /[0-9a-fA-F]/.test(line[j] ?? "")) {
      j++;
    }
    return j;
  }
  while (j < line.length && isDigit(line[j] ?? "")) {
    j++;
  }
  if (line[j] === "." && isDigit(line[j + 1] ?? "")) {
    j++;
    while (j < line.length && isDigit(line[j] ?? "")) {
      j++;
    }
  }
  if ((line[j] === "e" || line[j] === "E") && /^[+-]?\d/.test(line.slice(j + 1, j + 3))) {
    j += 2;
    while (j < line.length && isDigit(line[j] ?? "")) {
      j++;
    }
  }
  return j;
}

/** The dollar tag ("$$" or "$name$") that starts at i, or null. */
function dollarTag(line: string, i: number): string | null {
  const m = /^\$(?:[A-Za-z_][A-Za-z_0-9]*)?\$/.exec(line.slice(i, i + 40));
  return m ? m[0] : null;
}

function isParameterStart(line: string, i: number): boolean {
  const c = line[i] as string;
  const next = line[i + 1] ?? "";
  if (c === "$") {
    return isDigit(next);
  }
  return (c === ":" || c === "@") && isWordStart(next) && line[i - 1] !== ":";
}

/**
 * scanLine tokenizes one line (without its newline), given what was open at
 * its start, and returns the tokens and what is open at its end. The text of
 * the tokens is the line, whole.
 */
export function scanLine(line: string, state: LineState): { tokens: Token[]; end: LineState } {
  const tokens: Token[] = [];
  let i = 0;
  let end: LineState = startState;

  const push = (kind: TokenKind, from: number, to: number, continued?: boolean): void => {
    const token: Token = { kind, text: line.slice(from, to), start: from };
    if (continued) {
      token.continued = true;
    }
    tokens.push(token);
  };

  if (state.open !== "none") {
    let to: number;
    if (state.open === "block") {
      const k = line.indexOf("*/");
      to = k < 0 ? -1 : k + 2;
    } else if (state.open === "quote") {
      to = closeQuote(line, 0, state.quote);
    } else {
      const k = line.indexOf(state.tag);
      to = k < 0 ? -1 : k + state.tag.length;
    }
    const kind: TokenKind = state.open === "block" ? "comment" : state.open === "quote" && state.quote !== "'" ? "identifier" : "string";
    if (to < 0) {
      if (line.length > 0) {
        push(kind, 0, line.length, true);
      }
      return { tokens, end: state };
    }
    push(kind, 0, to, true);
    i = to;
  }

  while (i < line.length) {
    const c = line[i] as string;
    const next = line[i + 1] ?? "";

    if (isSpace(c)) {
      let j = i + 1;
      while (j < line.length && isSpace(line[j] as string)) {
        j++;
      }
      push("space", i, j);
      i = j;
    } else if (c === "-" && next === "-") {
      push("comment", i, line.length);
      i = line.length;
    } else if (c === "/" && next === "*") {
      const k = line.indexOf("*/", i + 2);
      if (k < 0) {
        push("comment", i, line.length);
        end = { open: "block" };
        i = line.length;
      } else {
        push("comment", i, k + 2);
        i = k + 2;
      }
    } else if (c === "'" || c === '"' || c === "`") {
      const j = closeQuote(line, i + 1, c);
      const kind: TokenKind = c === "'" ? "string" : "identifier";
      if (j < 0) {
        push(kind, i, line.length);
        end = { open: "quote", quote: c };
        i = line.length;
      } else {
        push(kind, i, j);
        i = j;
      }
    } else if (c === "$" && dollarTag(line, i) !== null) {
      const tag = dollarTag(line, i) as string;
      const k = line.indexOf(tag, i + tag.length);
      if (k < 0) {
        push("string", i, line.length);
        end = { open: "dollar", tag };
        i = line.length;
      } else {
        push("string", i, k + tag.length);
        i = k + tag.length;
      }
    } else if (isDigit(c) || (c === "." && isDigit(next))) {
      const j = numberEnd(line, i);
      push("number", i, j);
      i = j;
    } else if (isWordStart(c)) {
      let j = i + 1;
      while (j < line.length && isWordPart(line[j] as string)) {
        j++;
      }
      push(isKeyword(line.slice(i, j)) ? "keyword" : "identifier", i, j);
      i = j;
    } else if (isParameterStart(line, i)) {
      let j = i + 1;
      while (j < line.length && isWordPart(line[j] as string)) {
        j++;
      }
      push("identifier", i, j);
      i = j;
    } else if (symbolChars.includes(c)) {
      let j = i + 1;
      while (
        j < line.length &&
        symbolChars.includes(line[j] as string) &&
        !(line[j] === "-" && line[j + 1] === "-") &&
        !(line[j] === "/" && line[j + 1] === "*")
      ) {
        j++;
      }
      push("operator", i, j);
      i = j;
    } else {
      // Brackets, commas, dots and anything unknown stand alone, so nothing is ever skipped.
      push("operator", i, i + 1);
      i++;
    }
  }

  return { tokens, end };
}

/** tokenize returns every token of a text, with offsets into the text and a multi-line token in one piece. */
export function tokenize(text: string): Token[] {
  const out: Token[] = [];
  let state = startState;
  let offset = 0;

  for (const line of text.split("\n")) {
    const result = scanLine(line, state);
    let first = 0;

    if (state.open !== "none") {
      // The newline belongs to the token that was open.
      const last = out[out.length - 1];
      if (last) {
        last.text += "\n";
        const cont = result.tokens[0];
        if (cont?.continued) {
          last.text += cont.text;
          first = 1;
        }
      }
    } else if (offset > 0) {
      // A newline between tokens is a space token, so the tokens join back to the text.
      const last = out[out.length - 1];
      if (last && last.kind === "space") {
        last.text += "\n";
      } else {
        out.push({ kind: "space", text: "\n", start: offset - 1 });
      }
    }

    for (const t of result.tokens.slice(first)) {
      out.push({ kind: t.kind, text: t.text, start: offset + t.start });
    }

    state = result.end;
    offset += line.length + 1;
  }

  return out;
}

/** One line of the cache: its text, what was open at its start, and its tokens. */
interface CachedLine {
  text: string;
  start: LineState;
  tokens: Token[];
  end: LineState;
}

/**
 * LineCache tokenizes a text line by line and, when the text changes, redoes
 * only the lines that changed and those whose start state moved (a quote opened
 * above). Typing in a long text costs about one line.
 */
export class LineCache {
  private lines: CachedLine[] = [];

  /** update brings the cache to the text and returns the tokens of each line. */
  update(text: string): readonly (readonly Token[])[] {
    const next = text.split("\n");
    const out = new Array<CachedLine>(next.length);
    let state = startState;

    for (let i = 0; i < next.length; i++) {
      const line = next[i] as string;
      const old = this.lines[i];
      let cached: CachedLine;
      if (old && old.text === line && sameState(old.start, state)) {
        cached = old;
      } else {
        const { tokens, end } = scanLine(line, state);
        cached = { text: line, start: state, tokens, end };
      }
      out[i] = cached;
      state = cached.end;
    }

    this.lines = out;
    return out.map((l) => l.tokens);
  }
}
