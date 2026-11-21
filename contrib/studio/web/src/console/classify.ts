import { tokenize } from "../editor/tokenizer";

/**
 * The client's own reading of a statement, used to warn before sending and to
 * decide when to ask for a confirmation. The server stays the authority: it
 * refuses any write that was not asked for with --write, whatever this says.
 */

export interface StatementInfo {
  /** The verb in upper case; for a WITH, the write verb it carries. Empty for a blank statement. */
  kind: string;
  /** True when the statement is not a plain read (or reads into a table). */
  write: boolean;
  /** True when a write has no WHERE, or is a DROP or a TRUNCATE: it needs the source name typed. */
  destructive: boolean;
  /** Why the confirmation is stricter, in words; empty when it is not. */
  warning: string;
}

const readVerbs = new Set(["select", "with", "show", "explain", "describe", "desc", "table", "values", "pragma"]);
const writeVerbs = new Set(["insert", "update", "delete", "merge", "replace", "create", "drop", "alter", "truncate"]);

export function classify(sql: string): StatementInfo {
  const words: string[] = [];
  let first = "";
  let carried = "";
  let hasWhere = false;
  let hasInto = false;
  let sets = false;

  for (const t of tokenize(sql)) {
    if (t.kind === "space" || t.kind === "comment") {
      continue;
    }
    if (t.kind === "operator" && first === "" && t.text === "(") {
      continue;
    }
    if (t.kind === "operator" && t.text === "=") {
      sets = true;
    }
    if (t.kind !== "keyword" && t.kind !== "identifier") {
      continue;
    }
    const w = t.text.toLowerCase();
    words.push(w);
    if (first === "") {
      first = w;
    }
    // Quoted names are identifiers: a column called "update" is not a verb.
    const bare = t.kind === "keyword";
    if (bare && w === "where") {
      hasWhere = true;
    } else if (bare && w === "into") {
      hasInto = true;
    } else if (bare && first === "with" && carried === "" && writeVerbs.has(w)) {
      carried = w;
    }
  }

  const verb = carried !== "" ? carried : first;
  let write = !readVerbs.has(verb) || hasInto;
  if (verb === "pragma") {
    write = sets;
  }
  if (first === "") {
    write = false;
  }

  const kind = verb.toUpperCase();
  let warning = "";
  if (verb === "drop" || verb === "truncate") {
    warning = `${kind} removes data or structure that a rollback cannot bring back once it is committed.`;
  } else if ((verb === "update" || verb === "delete") && !hasWhere) {
    warning = `This ${kind} has no WHERE, so it changes every row of the table.`;
  }

  return { kind, write, destructive: warning !== "", warning };
}
