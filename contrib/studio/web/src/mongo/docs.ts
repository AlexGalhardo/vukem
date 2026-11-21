import type { ApiCell, Column } from "../api/types";
import { parseJSON } from "./json";
import type { Json } from "./json";

/** The first documents of a collection give its columns for the Table view. */
export const inferFrom = 100;
export const maxColumns = 60;
const cutAt = 300;

/** parseDoc reads a document as the server sent it (relaxed Extended JSON), or null when it cannot. */
export function parseDoc(text: string): { [key: string]: Json } | null {
  const r = parseJSON(text);
  return r.ok && r.value !== null && typeof r.value === "object" && !Array.isArray(r.value) ? r.value : null;
}

/** inferColumns lists the top-level field names of the first documents, "_id" first, then in order of appearance. */
export function inferColumns(docs: readonly ({ [key: string]: Json } | null)[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const d of docs.slice(0, inferFrom)) {
    if (d === null) continue;
    for (const k of Object.keys(d)) {
      if (!seen.has(k) && out.length < maxColumns) {
        seen.add(k);
        out.push(k);
      }
    }
  }
  const i = out.indexOf("_id");
  if (i > 0) {
    out.splice(i, 1);
    out.unshift("_id");
  }
  return out;
}

export function columnsOf(names: readonly string[]): Column[] {
  return names.map((name) => ({ name, type: "", nullable: true }));
}

const wrappers = ["$oid", "$date", "$numberLong", "$numberInt", "$numberDouble", "$numberDecimal", "$uuid", "$timestamp"] as const;

/** compact is the JSON of a value on one line. */
export function compact(v: Json): string {
  return JSON.stringify(v);
}

/** shown is a value as a person reads it: an ObjectId or a date shows its inner text, the rest is compact JSON. */
export function shown(v: Json): string {
  if (v !== null && typeof v === "object" && !Array.isArray(v)) {
    const keys = Object.keys(v);
    const key = keys.length === 1 ? keys[0] : undefined;
    if (key !== undefined && (wrappers as readonly string[]).includes(key)) {
      const inner = (v as { [k: string]: Json })[key] as Json;
      return typeof inner === "string" || typeof inner === "number" ? String(inner) : compact(inner);
    }
  }
  return typeof v === "string" ? v : compact(v);
}

/** cellOf makes the grid cell of a value: absent and null are NULL, nested values are compact JSON, long text is cut. */
export function cellOf(v: Json | undefined): ApiCell {
  if (v === undefined || v === null) {
    return { t: "", n: true };
  }
  const text = shown(v);
  const literal = typeof v === "number" || typeof v === "boolean";
  if (text.length > cutAt) {
    return { t: text.slice(0, cutAt), x: true };
  }
  return literal ? { t: text, l: true } : { t: text };
}

/** rowOf is the grid row of a document over the inferred columns. */
export function rowOf(columns: readonly string[], doc: { [key: string]: Json } | null): ApiCell[] {
  return columns.map((c) => cellOf(doc?.[c]));
}

/** The server reads a field path as names joined by dots. A column that is not one cannot be filtered or sorted. */
export const fieldPath = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)*$/;

export function canUseField(name: string): boolean {
  return fieldPath.test(name);
}

/**
 * confirmId is what a person types to confirm a write on a document, as the
 * server compares it: the hex of an ObjectId, the digits of an integer _id, the
 * text of a string _id, otherwise the canonical Extended JSON of the _id.
 */
export function confirmId(id: string): string {
  const r = parseJSON(id);
  if (!r.ok) return id;
  const v = r.value;
  if (typeof v === "string") return v;
  if (v !== null && typeof v === "object" && !Array.isArray(v)) {
    const keys = Object.keys(v);
    const key = keys[0];
    if (keys.length === 1 && key !== undefined && ["$oid", "$numberInt", "$numberLong"].includes(key)) {
      const inner = (v as { [k: string]: Json })[key];
      if (typeof inner === "string") return inner;
    }
  }
  return id;
}
