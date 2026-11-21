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
/** Verbs that are also scalar functions: followed by "(" they only read. */
const scalarFunctions = new Set(["replace", "insert", "truncate"]);

export function classify(sql: string): StatementInfo {
  const tokens = tokenize(sql).filter((t) => t.kind !== "space" && t.kind !== "comment");
  let first = "";
  let carried = "";
  let hasInto = false;
  let sets = false;
  let depth = 0;
  // The UPDATE or DELETE being read. Only a WHERE at its own depth filters it:
  // one inside a subquery or a CTE says nothing about the rows it changes.
  let open: { depth: number; filtered: boolean } | null = null;
  let unfiltered = false;

  const closeOpen = (): void => {
    if (open !== null && !open.filtered) {
      unfiltered = true;
    }
    open = null;
  };

  for (const [i, t] of tokens.entries()) {
    if (t.kind === "operator" && t.text === "(") {
      depth++;
      continue;
    }
    if (t.kind === "operator" && t.text === ")") {
      depth--;
      if (open !== null && depth < open.depth) {
        closeOpen();
      }
      continue;
    }
    if (t.kind === "operator" && t.text === "=") {
      sets = true;
    }
    if (t.kind !== "keyword" && t.kind !== "identifier") {
      continue;
    }
    const w = t.text.toLowerCase();
    if (first === "") {
      first = w;
    }
    // Quoted names are identifiers: a column called "update" is not a verb.
    const bare = t.kind === "keyword";
    const next = tokens[i + 1];
    const isCall = next?.kind === "operator" && next.text === "(" && scalarFunctions.has(w);
    if (bare && w === "where") {
      if (open !== null && depth === open.depth) {
        open.filtered = true;
      }
    } else if (bare && w === "into") {
      hasInto = true;
    } else if (bare && !isCall && writeVerbs.has(w)) {
      if (first === "with" && carried === "") {
        carried = w;
      }
      if (w === "update" || w === "delete") {
        closeOpen();
        open = { depth, filtered: false };
      }
    }
  }
  closeOpen();

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
  } else if ((verb === "update" || verb === "delete") && unfiltered) {
    warning = `This ${kind} has no WHERE, so it changes every row of the table.`;
  }

  return { kind, write, destructive: warning !== "", warning };
}
