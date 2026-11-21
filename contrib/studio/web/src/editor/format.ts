import { tokenize } from "./tokenizer";
import type { Token } from "./tokenizer";

/**
 * format re-indents a SQL text: keywords in upper case, one clause per line,
 * subqueries indented. Strings, quoted names and comments are kept exactly;
 * only the whitespace between tokens and the case of keywords change, so
 * formatting twice gives what formatting once gave.
 */

const indentUnit = "  ";
const symbolChars = "+-*/%=<>!|&^~:@#?";

/** Clause keywords that start a line inside a statement or a subquery. */
const clauseWords = new Set(["select", "from", "where", "having", "limit", "offset", "union", "intersect", "except", "values", "set", "returning", "window"]);
const joinModifiers = new Set(["left", "right", "inner", "full", "cross", "natural", "outer"]);
/** Keywords that are called like functions: no space before the parenthesis. */
const callWords = new Set(["cast", "replace", "left", "right", "if", "row"]);
/** After these a SELECT or a FROM continues the line instead of starting one. */
const keepOnLine = new Set(["explain", "analyze", "analyse", "plan", "verbose", "delete", "distinct"]);
const conditionClauses = new Set(["where", "having", "on"]);

interface Frame {
  /** A subquery (or the statement itself): its clauses start lines. */
  sub: boolean;
  indent: number;
  clause: string;
}

function isUnaryContext(prev: Token | undefined): boolean {
  if (!prev) {
    return true;
  }
  if (prev.kind === "operator") {
    return prev.text !== ")" && prev.text !== "]";
  }
  if (prev.kind === "keyword") {
    return !["null", "true", "false", "end"].includes(prev.text.toLowerCase());
  }
  return false;
}

function spaceBefore(prev: Token | undefined, tok: Token, unaryPrev: boolean): boolean {
  if (!prev) {
    return false;
  }
  const t = tok.text;
  const p = prev.text;
  if (t === "," || t === ";" || t === ")" || t === "." || t === "]" || t === "::") {
    return false;
  }
  if (p === "." || p === "(" || p === "[" || p === "::" || unaryPrev) {
    return false;
  }
  if (t === "(") {
    const called = (prev.kind === "identifier" && prev.text !== "") || (prev.kind === "keyword" && callWords.has(p.toLowerCase()));
    return !(called || p === ")" || p === "]");
  }
  if (t === "[") {
    return !(prev.kind === "identifier" || p === "]" || p === ")");
  }
  return true;
}

/** True when joining the two texts with no space would read as other tokens. */
function mustSeparate(before: string, after: string): boolean {
  const a = before[before.length - 1] ?? "";
  const b = after[0] ?? "";
  if (symbolChars.includes(a) && symbolChars.includes(b)) {
    return true;
  }
  if (before === "." && /\d/.test(b)) {
    return true;
  }
  return /[\w$]/.test(a) && /[\w$]/.test(b);
}

export function format(sql: string): string {
  const tokens: Token[] = tokenize(sql).filter((t) => t.kind !== "space");
  if (tokens.length === 0) {
    return "";
  }

  const lines: string[] = [];
  let line = "";
  let lineIndent = 0;
  const frames: Frame[] = [{ sub: true, indent: 0, clause: "" }];
  let prev: Token | undefined;
  let unaryPrev = false;
  let betweenOpen = false;

  const flush = (nextIndent: number): void => {
    if (line !== "") {
      lines.push(indentUnit.repeat(lineIndent) + line);
    }
    line = "";
    lineIndent = nextIndent;
  };

  const top = (): Frame => frames[frames.length - 1] as Frame;

  const isJoinStart = (i: number): boolean => {
    const word = (tokens[i]?.text ?? "").toLowerCase();
    if (tokens[i]?.kind !== "keyword") {
      return false;
    }
    if (word === "join") {
      return !joinModifiers.has((tokens[i - 1]?.text ?? "").toLowerCase()) || tokens[i - 1]?.kind !== "keyword";
    }
    if (!joinModifiers.has(word) || joinModifiers.has((tokens[i - 1]?.text ?? "").toLowerCase())) {
      return false;
    }
    for (let j = i + 1; j < tokens.length; j++) {
      const w = (tokens[j]?.text ?? "").toLowerCase();
      if (w === "join") {
        return true;
      }
      if (!joinModifiers.has(w)) {
        return false;
      }
    }
    return false;
  };

  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i] as Token;
    const word = tok.kind === "keyword" ? tok.text.toLowerCase() : "";
    const frame = top();
    const prevWord = prev && prev.kind === "keyword" ? prev.text.toLowerCase() : "";
    let breakTo: number | null = null;

    if (word !== "" && frame.sub) {
      if (clauseWords.has(word)) {
        const continues = (word === "select" || word === "from") && keepOnLine.has(prevWord);
        if (!continues) {
          breakTo = frame.indent;
        }
        frame.clause = word;
      } else if ((word === "group" || word === "order") && tokens[i + 1]?.text.toLowerCase() === "by") {
        breakTo = frame.indent;
        frame.clause = word;
      } else if (isJoinStart(i)) {
        breakTo = frame.indent;
        frame.clause = "join";
      } else if (word === "on") {
        frame.clause = "on";
      } else if ((word === "and" || word === "or") && conditionClauses.has(frame.clause)) {
        if (word === "and" && betweenOpen) {
          betweenOpen = false;
        } else {
          breakTo = frame.indent + 1;
        }
      }
    }
    if (word === "between") {
      betweenOpen = true;
    }

    const afterLineComment = prev?.kind === "comment" && prev.text.startsWith("--");
    if (line !== "" && (breakTo !== null || afterLineComment)) {
      flush(breakTo ?? frame.indent + 1);
    } else if (line === "" && breakTo !== null) {
      lineIndent = breakTo;
    }

    const isClose = tok.text === ")" && tok.kind === "operator";
    if (isClose && frames.length > 1) {
      const closed = frames.pop() as Frame;
      if (closed.sub) {
        flush(top().indent);
      }
    }

    const text = tok.kind === "keyword" ? tok.text.toUpperCase() : tok.text;
    const wantsSpace = line !== "" && spaceBefore(prev, tok, unaryPrev);
    const joined = line !== "" && !wantsSpace && mustSeparate(prev?.text ?? "", text);
    if (wantsSpace || joined) {
      line += " ";
    }
    line += text;

    unaryPrev = tok.kind === "operator" && (tok.text === "-" || tok.text === "+") && isUnaryContext(prev);

    if (tok.kind === "operator" && tok.text === "(") {
      const next = tokens[i + 1]?.text.toLowerCase() ?? "";
      const sub = tokens[i + 1]?.kind === "keyword" && (next === "select" || next === "with" || next === "values");
      frames.push({ sub, indent: frame.indent + (sub ? 1 : 0), clause: "" });
      if (sub) {
        flush(top().indent);
      }
    } else if (tok.kind === "operator" && tok.text === ";") {
      frames.length = 1;
      (frames[0] as Frame).clause = "";
      betweenOpen = false;
      if (i < tokens.length - 1) {
        flush(0);
      }
    }

    prev = tok;
  }

  flush(0);

  return lines.join("\n");
}
