import type { Column } from "../api/types";

/** The operators the server accepts (see Filter in the Go package). */
export const operators = ["eq", "ne", "lt", "lte", "gt", "gte", "contains", "starts", "null", "notnull"] as const;

export type Operator = (typeof operators)[number];

export interface Filter {
  column: string;
  op: Operator;
  value: string;
}

export interface ViewState {
  filters: Filter[];
  /** The sort column, or null for the primary key order. */
  sort: string | null;
  desc: boolean;
}

export const emptyView: ViewState = { filters: [], sort: null, desc: false };

export const operatorLabels: Record<Operator, string> = {
  eq: "is",
  ne: "is not",
  lt: "is less than",
  lte: "is at most",
  gt: "is greater than",
  gte: "is at least",
  contains: "contains",
  starts: "starts with",
  null: "is empty (NULL)",
  notnull: "is not empty (not NULL)",
};

export type ColumnKind = "text" | "number" | "date" | "boolean" | "json" | "binary";

const numberTypes = new Set([
  "int", "integer", "tinyint", "smallint", "mediumint", "bigint", "int2", "int4", "int8", "serial", "smallserial",
  "bigserial", "real", "float", "float4", "float8", "double", "numeric", "decimal", "number", "money",
]);
const dateTypes = new Set(["date", "datetime", "timestamp", "timestamptz", "time", "timetz", "datetime2"]);
const binaryTypes = new Set(["blob", "bytea", "binary", "varbinary", "tinyblob", "mediumblob", "longblob"]);

/** columnKind groups a declared SQL type the way the grid and the filter bar need. */
export function columnKind(type: string): ColumnKind {
  const t = type.trim().toLowerCase();
  const cut = t.search(/[( ]/);
  const base = cut > 0 ? t.slice(0, cut) : t;

  if (numberTypes.has(base)) {
    return "number";
  }
  if (base === "bool" || base === "boolean") {
    return "boolean";
  }
  if (dateTypes.has(base)) {
    return "date";
  }
  if (base === "json" || base === "jsonb") {
    return "json";
  }
  if (binaryTypes.has(base)) {
    return "binary";
  }
  return "text";
}

const byKind: Record<ColumnKind, readonly Operator[]> = {
  text: ["contains", "starts", "eq", "ne", "null", "notnull"],
  number: ["eq", "ne", "lt", "lte", "gt", "gte", "null", "notnull"],
  date: ["eq", "ne", "lt", "lte", "gt", "gte", "null", "notnull"],
  boolean: ["eq", "ne", "null", "notnull"],
  json: ["contains", "null", "notnull"],
  binary: ["null", "notnull"],
};

/** operatorsFor lists the operators offered for a column kind, the first being the default. */
export function operatorsFor(kind: ColumnKind): readonly Operator[] {
  return byKind[kind];
}

/** needsValue is false for the operators that test for NULL. */
export function needsValue(op: Operator): boolean {
  return op !== "null" && op !== "notnull";
}

export function isOperator(s: string): s is Operator {
  return (operators as readonly string[]).includes(s);
}

/**
 * parseFilter reads "column:op:value". The value is everything after the
 * second colon, so it may hold colons. A filter with an unknown operator, no
 * column or no operator is refused (null): dropping it silently would show
 * every row as if it matched.
 */
export function parseFilter(raw: string): Filter | null {
  const first = raw.indexOf(":");
  if (first <= 0) {
    return null;
  }
  const rest = raw.slice(first + 1);
  const second = rest.indexOf(":");
  const op = second === -1 ? rest : rest.slice(0, second);
  const value = second === -1 ? "" : rest.slice(second + 1);

  if (!isOperator(op)) {
    return null;
  }
  return { column: raw.slice(0, first), op, value: needsValue(op) ? value : "" };
}

/** formatFilter is the inverse of parseFilter. */
export function formatFilter(f: Filter): string {
  return `${f.column}:${f.op}:${f.value}`;
}

/** canFilter is false for a column the "column:op:value" form cannot name. */
export function canFilter(column: string): boolean {
  return column !== "" && !column.includes(":");
}

/** validateFilter checks a filter against the table's columns and the operator rules of the column type. */
export function validateFilter(f: Filter, columns: readonly Column[]): string | null {
  const col = columns.find((c) => c.name === f.column);
  if (!col) {
    return `Unknown column ${f.column}.`;
  }
  if (!operatorsFor(columnKind(col.type)).includes(f.op)) {
    return `${f.column} cannot be filtered with "${operatorLabels[f.op]}".`;
  }
  if (needsValue(f.op) && f.value === "") {
    return `Give a value for ${f.column}.`;
  }
  return null;
}

/**
 * viewToSearch writes the state as a query string (with "?"), or "" for the
 * default view. open is the packed key of the row whose drawer is open.
 */
export function viewToSearch(view: ViewState, open?: string | null): string {
  const params = new URLSearchParams();
  for (const f of view.filters) {
    params.append("f", formatFilter(f));
  }
  if (view.sort !== null) {
    params.set("sort", view.sort);
    if (view.desc) {
      params.set("desc", "1");
    }
  }
  if (open) {
    params.set("open", open);
  }
  const s = params.toString();
  return s === "" ? "" : `?${s}`;
}

/** openFromSearch reads the packed key of the open row, or null. */
export function openFromSearch(search: string): string | null {
  const open = new URLSearchParams(search).get("open");
  return open === null || open === "" ? null : open;
}

export interface ParsedView {
  view: ViewState;
  /** How many f parameters were refused; the view says so instead of hiding it. */
  rejected: number;
}

/** searchToView reads a query string; filters that do not parse are counted, not applied. */
export function searchToView(search: string): ParsedView {
  const params = new URLSearchParams(search);
  const filters: Filter[] = [];
  let rejected = 0;

  for (const raw of params.getAll("f")) {
    const f = parseFilter(raw);
    if (f) {
      filters.push(f);
    } else {
      rejected++;
    }
  }

  const sort = params.get("sort");
  return {
    view: { filters, sort: sort === null || sort === "" ? null : sort, desc: sort !== null && sort !== "" && params.get("desc") === "1" },
    rejected,
  };
}

/** apiQuery is the query string of a rows or count request for the view (filters and sort only). */
export function apiQuery(view: ViewState, extra: Record<string, string> = {}): string {
  const params = new URLSearchParams();
  for (const f of view.filters) {
    params.append("filter", formatFilter(f));
  }
  if (view.sort !== null) {
    params.set("sort", view.sort);
    if (view.desc) {
      params.set("desc", "1");
    }
  }
  for (const [k, v] of Object.entries(extra)) {
    params.set(k, v);
  }
  const s = params.toString();
  return s === "" ? "" : `?${s}`;
}

/** nextSort cycles a column header: ascending, then descending, then none. */
export function nextSort(view: ViewState, column: string): ViewState {
  if (view.sort !== column) {
    return { ...view, sort: column, desc: false };
  }
  if (!view.desc) {
    return { ...view, sort: column, desc: true };
  }
  return { ...view, sort: null, desc: false };
}

/** sameView compares two views by value. */
export function sameView(a: ViewState, b: ViewState): boolean {
  return viewToSearch(a) === viewToSearch(b);
}
