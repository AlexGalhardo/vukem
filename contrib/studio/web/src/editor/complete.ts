import { keywords, tokenize } from "./tokenizer";
import type { Token } from "./tokenizer";

/**
 * Completion for the SQL editor. It reads only what the page already has: the
 * keywords, the table names of the source and the columns of tables the
 * statement mentions. Nothing is sent anywhere.
 */

export type SuggestionKind = "keyword" | "table" | "column" | "stage";

export interface Suggestion {
  label: string;
  kind: SuggestionKind;
}

export interface Completion {
  /** Offset where the word being completed starts: the accepted label replaces text[from, cursor). */
  from: number;
  items: Suggestion[];
}

export interface CompletionSource {
  tables: readonly string[];
  /** The columns of a table when they are known; undefined when not loaded (yet). */
  columns: (table: string) => readonly string[] | undefined;
}

export const maxSuggestions = 12;

const wordChar = /[A-Za-z0-9_$\u0080-￿]/;
const sortedKeywords = [...keywords].sort();

/** wordBefore is the word that ends at the cursor: its start offset and text. */
export function wordBefore(text: string, cursor: number): { from: number; word: string } {
  let from = cursor;
  while (from > 0 && wordChar.test(text[from - 1] as string)) {
    from--;
  }
  return { from, word: text.slice(from, cursor) };
}

interface TableRef {
  table: string;
  alias: string | null;
}

/** tablesIn finds "FROM t", "JOIN t AS x" and "UPDATE t" in the statement the cursor is in. */
export function tablesIn(text: string, cursor: number, known: readonly string[]): TableRef[] {
  const knownByLower = new Map(known.map((t) => [t.toLowerCase(), t]));
  const unquote = (s: string): string => s.replace(/^["`]|["`]$/g, "");
  const toks: Token[] = tokenize(text).filter((t) => t.kind !== "space" && t.kind !== "comment");

  // The statement is the span between semicolons around the cursor.
  let lo = 0;
  let hi = toks.length;
  toks.forEach((t, i) => {
    if (t.text === ";" && t.kind === "operator") {
      if (t.start < cursor) {
        lo = i + 1;
      } else if (hi === toks.length) {
        hi = i;
      }
    }
  });

  const refs: TableRef[] = [];
  for (let i = lo; i < hi; i++) {
    const w = (toks[i] as Token).text.toLowerCase();
    if (!["from", "join", "update", "into"].includes(w) || (toks[i] as Token).kind !== "keyword") {
      continue;
    }
    let j = i + 1;
    // A comma list after FROM: "from a, b".
    for (;;) {
      const nameTok = toks[j];
      if (!nameTok || j >= hi || (nameTok.kind !== "identifier" && nameTok.kind !== "keyword")) {
        break;
      }
      const table = knownByLower.get(unquote(nameTok.text).toLowerCase());
      if (nameTok.kind === "keyword" || !table) {
        break;
      }
      let k = j + 1;
      let alias: string | null = null;
      if (toks[k]?.kind === "keyword" && toks[k]?.text.toLowerCase() === "as") {
        k++;
      }
      const maybe = toks[k];
      if (k < hi && maybe && maybe.kind === "identifier") {
        alias = unquote(maybe.text);
        k++;
      }
      refs.push({ table, alias });
      if (w === "from" && toks[k]?.text === ",") {
        j = k + 1;
        continue;
      }
      break;
    }
  }
  return refs;
}

function rank(label: string, prefix: string): number {
  const l = label.toLowerCase();
  return l === prefix ? 0 : l.startsWith(prefix) ? 1 : l.includes(prefix) ? 2 : -1;
}

function pick(items: Suggestion[], prefix: string): Suggestion[] {
  const p = prefix.toLowerCase();
  const scored: { item: Suggestion; score: number }[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    const key = item.kind + ":" + item.label.toLowerCase();
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    const score = p === "" ? 0 : rank(item.label, p);
    if (score >= 0) {
      scored.push({ item, score });
    }
  }
  // Stable sort: prefix matches first, then in the order given (tables and columns before keywords).
  scored.sort((a, b) => a.score - b.score);
  const out = scored.slice(0, maxSuggestions).map((s) => s.item);
  // A word typed in full, with nothing else it could become, needs no list.
  if (p !== "" && out.length === 1 && (out[0] as Suggestion).label.toLowerCase() === p) {
    return [];
  }
  return out;
}

function insideLiteral(text: string, cursor: number): boolean {
  for (const t of tokenize(text)) {
    if ((t.kind === "string" || t.kind === "comment") && cursor > t.start && cursor <= t.start + t.text.length) {
      // The cursor right after the end of a closed block comment or string is outside it.
      const closed =
        (t.kind === "string" && t.text.length > 1 && /['$]$/.test(t.text)) || (t.kind === "comment" && t.text.endsWith("*/"));
      return !(closed && cursor === t.start + t.text.length);
    }
  }
  return false;
}

/** How many lines around the cursor completion reads. A statement is rarely longer, and reading all of a long script on every key would not be cheap. */
export const windowLines = 100;
const windowFloor = 20_000;

/** windowAround is the part of the text within windowLines lines of the cursor, and where it starts. */
export function windowAround(text: string, cursor: number): { text: string; offset: number } {
  if (text.length <= windowFloor) {
    return { text, offset: 0 };
  }
  let start = cursor;
  for (let n = 0; n < windowLines && start > 0; n++) {
    const k = text.lastIndexOf("\n", start - 1);
    start = k < 0 ? 0 : k;
  }
  if (start > 0) {
    start++;
  }
  let end = cursor;
  for (let n = 0; n < windowLines && end < text.length; n++) {
    const k = text.indexOf("\n", n === 0 ? end : end + 1);
    end = k < 0 ? text.length : k;
  }
  return { text: text.slice(start, end), offset: start };
}

/** complete returns the suggestions for the word at the cursor, or none when there is nothing to offer. */
export function complete(text: string, cursor: number, source: CompletionSource, explicit = false): Completion {
  const w = windowAround(text, cursor);
  const result = completeIn(w.text, cursor - w.offset, source, explicit);
  return { from: result.from + w.offset, items: result.items };
}

function completeIn(text: string, cursor: number, source: CompletionSource, explicit: boolean): Completion {
  const { from, word } = wordBefore(text, cursor);
  const none: Completion = { from, items: [] };

  if (insideLiteral(text, cursor)) {
    return none;
  }

  const refs = tablesIn(text, cursor, source.tables);
  const columnsOf = (table: string): Suggestion[] => (source.columns(table) ?? []).map((label) => ({ label, kind: "column" }));

  // "table." or "alias." offers the columns of that table.
  if (text[from - 1] === ".") {
    const q = wordBefore(text, from - 1);
    const qualifier = q.word.toLowerCase();
    const ref =
      refs.find((r) => r.alias?.toLowerCase() === qualifier) ??
      refs.find((r) => r.table.toLowerCase() === qualifier) ??
      (source.tables.find((t) => t.toLowerCase() === qualifier) ? { table: source.tables.find((t) => t.toLowerCase() === qualifier) as string } : undefined);
    return { from, items: ref ? pick(columnsOf(ref.table), word) : [] };
  }

  if (word === "" && !explicit) {
    return none;
  }

  const before = tokenize(text.slice(Math.max(0, from - 400), from))
    .filter((t) => t.kind !== "space" && t.kind !== "comment")
    .at(-1);
  const afterTableWord = before?.kind === "keyword" && ["from", "join", "update", "into", "table"].includes(before.text.toLowerCase());

  const tables: Suggestion[] = source.tables.map((label) => ({ label, kind: "table" }));
  const columns = refs.flatMap((r) => columnsOf(r.table));
  const words: Suggestion[] = sortedKeywords.map((label) => ({ label: label.toUpperCase(), kind: "keyword" }));

  const items = afterTableWord ? [...tables, ...words] : [...columns, ...tables, ...words];
  return { from, items: pick(items, word) };
}
