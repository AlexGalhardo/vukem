import { tokenize } from "./tokenizer";
import type { Token } from "./tokenizer";

/** Text operations of the editor, pure so they can be tested without a browser. */

export interface Range {
  start: number;
  end: number;
}

/** An edit: replace text[from, to) with insert, then select [selStart, selEnd). */
export interface Edit {
  from: number;
  to: number;
  insert: string;
  selStart: number;
  selEnd: number;
}

export const indentUnit = "  ";
export const tabSize = 4;

function isBlank(tokens: readonly Token[]): boolean {
  return tokens.every((t) => t.kind === "space" || t.kind === "comment");
}

/**
 * statementAt returns the span of the statement the cursor is in, without the
 * semicolon and the whitespace around it. Statements end at a semicolon that is
 * not inside a string or a comment. A cursor in a blank stretch takes the
 * statement before it, or the one after when nothing comes before.
 */
export function statementAt(text: string, cursor: number): Range | null {
  const tokens = tokenize(text);
  const spans: { start: number; end: number; semi: number }[] = [];
  let start = 0;

  for (const t of tokens) {
    if (t.kind === "operator" && t.text === ";") {
      spans.push({ start, end: t.start, semi: t.start + 1 });
      start = t.start + 1;
    }
  }
  spans.push({ start, end: text.length, semi: text.length });

  const trimmed = (s: { start: number; end: number }): Range | null => {
    const inside = tokens.filter((t) => t.start >= s.start && t.start < s.end);
    if (isBlank(inside)) {
      return null;
    }
    const first = inside.find((t) => t.kind !== "space");
    const last = [...inside].reverse().find((t) => t.kind !== "space");
    if (!first || !last) {
      return null;
    }
    return { start: first.start, end: Math.min(last.start + last.text.length, s.end) };
  };

  const at = spans.findIndex((s) => cursor >= s.start && cursor <= s.semi);
  const index = at < 0 ? spans.length - 1 : at;
  const found = trimmed(spans[index] as { start: number; end: number });
  if (found) {
    return found;
  }
  for (let i = index - 1; i >= 0; i--) {
    const r = trimmed(spans[i] as { start: number; end: number });
    if (r) {
      return r;
    }
  }
  for (let i = index + 1; i < spans.length; i++) {
    const r = trimmed(spans[i] as { start: number; end: number });
    if (r) {
      return r;
    }
  }
  return null;
}

/** The start offset of the line that holds offset, and the offset of its end (before the newline). */
export function lineBounds(text: string, offset: number): Range {
  const start = text.lastIndexOf("\n", offset - 1) + 1;
  const nl = text.indexOf("\n", offset);
  return { start, end: nl < 0 ? text.length : nl };
}

/** lineOf converts an offset to a zero-based line and column. */
export function lineOf(text: string, offset: number): { line: number; col: number } {
  let line = 0;
  let from = 0;
  for (;;) {
    const nl = text.indexOf("\n", from);
    if (nl < 0 || nl >= offset) {
      break;
    }
    line++;
    from = nl + 1;
  }
  return { line, col: offset - from };
}

/** The lines that a selection touches; a selection that ends at the start of a line leaves that line out. */
function touchedLines(text: string, selStart: number, selEnd: number): Range {
  const first = lineBounds(text, selStart);
  const endOffset = selEnd > selStart && text[selEnd - 1] === "\n" ? selEnd - 1 : selEnd;
  const last = lineBounds(text, endOffset);
  return { start: first.start, end: last.end };
}

/** indentEdit indents the lines of the selection by one unit, or outdents them. A caret with no selection inserts the unit. */
export function indentEdit(text: string, selStart: number, selEnd: number, outdent: boolean): Edit | null {
  if (selStart === selEnd && !outdent) {
    return { from: selStart, to: selEnd, insert: indentUnit, selStart: selStart + indentUnit.length, selEnd: selStart + indentUnit.length };
  }
  const { start, end } = touchedLines(text, selStart, selEnd);
  const lines = text.slice(start, end).split("\n");
  let firstShift = 0;
  let total = 0;
  const changed = lines.map((l, i) => {
    let out: string;
    if (outdent) {
      const m = /^(?: {1,2}|\t)/.exec(l);
      out = m ? l.slice(m[0].length) : l;
    } else {
      out = l === "" ? l : indentUnit + l;
    }
    const delta = out.length - l.length;
    if (i === 0) {
      firstShift = delta;
    }
    total += delta;
    return out;
  });
  if (total === 0) {
    return null;
  }
  const insert = changed.join("\n");
  const lineStart = start;
  return {
    from: start,
    to: end,
    insert,
    selStart: Math.max(selStart + firstShift, lineStart),
    selEnd: Math.max(selEnd + total, lineStart),
  };
}

/** commentEdit toggles "--" comments on the lines of the selection: all commented lines are uncommented, otherwise every line is commented. */
export function commentEdit(text: string, selStart: number, selEnd: number): Edit {
  const { start, end } = touchedLines(text, selStart, selEnd);
  const lines = text.slice(start, end).split("\n");
  const filled = lines.filter((l) => l.trim() !== "");
  const allCommented = filled.length > 0 && filled.every((l) => /^\s*--/.test(l));
  let firstShift = 0;
  let total = 0;

  let changed: string[];
  if (allCommented) {
    changed = lines.map((l) => (l.trim() === "" ? l : l.replace(/^(\s*)-- ?/, "$1")));
  } else {
    const indent = Math.min(...filled.map((l) => (/^\s*/.exec(l) as RegExpExecArray)[0].length), Number.MAX_SAFE_INTEGER);
    const col = indent === Number.MAX_SAFE_INTEGER ? 0 : indent;
    changed = lines.map((l) => (l.trim() === "" && filled.length > 0 ? l : l.slice(0, col) + "-- " + l.slice(col)));
  }
  changed.forEach((l, i) => {
    const delta = l.length - (lines[i] as string).length;
    if (i === 0) {
      firstShift = delta;
    }
    total += delta;
  });
  return {
    from: start,
    to: end,
    insert: changed.join("\n"),
    selStart: Math.max(selStart + firstShift, start),
    selEnd: Math.max(selEnd + total, start),
  };
}

/** newlineEdit breaks the line at the caret and keeps the indentation of the line it leaves. */
export function newlineEdit(text: string, selStart: number, selEnd: number): Edit {
  const { start } = lineBounds(text, selStart);
  const indent = (/^[ \t]*/.exec(text.slice(start, selStart)) as RegExpExecArray)[0];
  const insert = "\n" + indent;
  return { from: selStart, to: selEnd, insert, selStart: selStart + insert.length, selEnd: selStart + insert.length };
}

export interface Match {
  line: number;
  col: number;
  len: number;
  /** Offset in the text. */
  index: number;
}

/** findMatches lists the case-insensitive matches of a query that has no newline. */
export function findMatches(text: string, query: string): Match[] {
  if (query === "" || query.includes("\n")) {
    return [];
  }
  const needle = query.toLowerCase();
  const matches: Match[] = [];
  let offset = 0;
  text.split("\n").forEach((line, i) => {
    const hay = line.toLowerCase();
    for (let at = hay.indexOf(needle); at >= 0; at = hay.indexOf(needle, at + needle.length)) {
      matches.push({ line: i, col: at, len: needle.length, index: offset + at });
    }
    offset += line.length + 1;
  });
  return matches;
}

/** visualColumn is the column of a character index with tabs expanded to the tab size. */
export function visualColumn(line: string, index: number): number {
  let col = 0;
  for (let i = 0; i < index && i < line.length; i++) {
    col += line[i] === "\t" ? tabSize - (col % tabSize) : 1;
  }
  return col;
}

export interface Position {
  line: number;
  col: number;
}

const pairs: Record<string, string> = { "(": ")", "[": "]", "{": "}" };
const closers: Record<string, string> = { ")": "(", "]": "[", "}": "{" };

/**
 * bracketMatch finds the bracket that pairs with the one next to the caret
 * (before it, else after it), ignoring brackets in strings and comments.
 */
export function bracketMatch(lines: readonly (readonly Token[])[], at: Position): [Position, Position] | null {
  const bracketAt = (col: number): Token | undefined =>
    lines[at.line]?.find((t) => t.kind === "operator" && t.start === col && (t.text in pairs || t.text in closers));
  const here = bracketAt(at.col - 1) ?? bracketAt(at.col);
  if (!here) {
    return null;
  }
  const origin: Position = { line: at.line, col: here.start };
  const forward = here.text in pairs;
  const open = forward ? here.text : (closers[here.text] as string);
  const close = forward ? (pairs[here.text] as string) : here.text;
  let depth = 0;

  for (let l = at.line; l >= 0 && l < lines.length; l += forward ? 1 : -1) {
    const toks = lines[l] as readonly Token[];
    const ordered = forward ? toks : [...toks].reverse();
    for (const t of ordered) {
      if (t.kind !== "operator" || (t.text !== open && t.text !== close)) {
        continue;
      }
      if (l === origin.line && t.start === origin.col) {
        depth = 1;
        continue;
      }
      if (depth === 0) {
        continue;
      }
      depth += t.text === (forward ? open : close) ? 1 : -1;
      if (depth === 0) {
        return [origin, { line: l, col: t.start }];
      }
    }
  }
  return null;
}
