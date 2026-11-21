import type { Completion } from "../editor/complete";

/** The aggregation stages Studio offers to complete: a fixed list; $out and $merge are listed because the server decides about them. */
export const stageNames: readonly string[] = [
  "$addFields", "$bucket", "$bucketAuto", "$count", "$facet", "$geoNear", "$graphLookup", "$group", "$limit",
  "$lookup", "$match", "$merge", "$out", "$project", "$replaceRoot", "$replaceWith", "$sample", "$set", "$skip",
  "$sort", "$sortByCount", "$unionWith", "$unset", "$unwind",
];

/** The comparison and logic operators of a filter. Script operators are not here on purpose. */
export const filterOperators: readonly string[] = [
  "$and", "$or", "$nor", "$not", "$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin", "$exists", "$type",
  "$regex", "$size", "$all", "$elemMatch", "$mod",
];

/** The top-level fields of the console statement. */
export const statementFields: readonly string[] = ["collection", "filter", "pipeline"];

export const maxSuggestions = 12;

const wordChar = /[A-Za-z0-9_$.]/;

/** A name inside a string: where the caret sits in a JSON string, and the key of that string's value if any. */
function stringContext(text: string, caret: number): { from: number; key: string | null } | null {
  const lineStart = text.lastIndexOf("\n", caret - 1) + 1;
  const line = text.slice(lineStart, caret);
  let open = -1;
  for (let i = 0; i < line.length; i++) {
    if (line[i] === "\\") {
      i++;
    } else if (line[i] === '"') {
      open = open === -1 ? i : -1;
    }
  }
  if (open === -1) {
    return null;
  }
  const before = line.slice(0, open);
  const key = /"([^"]+)"\s*:\s*$/.exec(before);
  return { from: lineStart + open + 1, key: key ? (key[1] as string) : null };
}

/**
 * completeJson completes inside the console statement: stage and operator
 * names where a $-word is typed, collection names in the value of
 * "collection" (and of "from" and "coll" of $lookup and its kin), and the
 * three top-level field names. Everything comes from lists the page has.
 */
export function completeJson(text: string, caret: number, collections: readonly string[], explicit: boolean): Completion {
  const none: Completion = { from: caret, items: [] };
  const ctx = stringContext(text, caret);
  if (ctx === null) {
    return none;
  }
  const typed = text.slice(ctx.from, caret);
  const lower = typed.toLowerCase();

  if (typed.startsWith("$")) {
    const names = [...new Set([...stageNames, ...filterOperators])].sort();
    return {
      from: ctx.from,
      items: names.filter((n) => n.toLowerCase().startsWith(lower)).slice(0, maxSuggestions).map((label) => ({ label, kind: "stage" as const })),
    };
  }
  if (ctx.key === "collection" || ctx.key === "from" || ctx.key === "coll" || ctx.key === "into") {
    return {
      from: ctx.from,
      items: collections.filter((c) => c.toLowerCase().startsWith(lower)).slice(0, maxSuggestions).map((label) => ({ label, kind: "table" as const })),
    };
  }
  if (ctx.key === null && (explicit || typed !== "") && /^[A-Za-z]*$/.test(typed)) {
    return {
      from: ctx.from,
      items: statementFields.filter((n) => n.startsWith(lower)).map((label) => ({ label, kind: "column" as const })),
    };
  }
  return none;
}

export { wordChar };
