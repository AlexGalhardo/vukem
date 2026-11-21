import type { ApiCell, Column, QueryResponse } from "../api/types";
import type { GridData } from "../grid/useGridData";

/** The grid wants typed columns; a console result only has names, so every column reads as text. */
export function resultColumns(names: readonly string[]): Column[] {
  return names.map((name) => ({ name, type: "", nullable: true }));
}

/** staticGridData lets the grid draw rows that are all in memory: nothing is paged, counted or edited. */
export function staticGridData(rows: readonly (readonly ApiCell[])[], version: number): GridData {
  return {
    rowCount: rows.length,
    approx: false,
    capped: false,
    mode: "offset",
    tail: false,
    row: (index) => rows[index] as ApiCell[] | undefined,
    version,
    want: () => undefined,
    loading: false,
    error: null,
    retry: () => undefined,
    refresh: () => undefined,
    countState: "idle",
    exact: rows.length,
    askCount: () => undefined,
    jumpToEnd: () => Promise.resolve(rows.length),
    leaveTail: () => undefined,
    patchCell: () => undefined,
    reload: () => undefined,
  };
}

function compareCells(a: ApiCell | undefined, b: ApiCell | undefined): number {
  // NULL sorts first, as in most databases when ascending.
  if (a?.n && b?.n) {
    return 0;
  }
  if (a?.n) {
    return -1;
  }
  if (b?.n) {
    return 1;
  }
  const x = a?.t ?? "";
  const y = b?.t ?? "";
  const nx = Number(x);
  const ny = Number(y);
  if (x.trim() !== "" && y.trim() !== "" && Number.isFinite(nx) && Number.isFinite(ny)) {
    return nx - ny;
  }
  return x < y ? -1 : x > y ? 1 : 0;
}

/** sortRows returns a sorted copy of the rows by one column (numbers as numbers), or the rows as they are. */
export function sortRows(
  rows: readonly (readonly ApiCell[])[],
  column: number | null,
  desc: boolean,
): readonly (readonly ApiCell[])[] {
  if (column === null || column < 0) {
    return rows;
  }
  const sign = desc ? -1 : 1;
  return rows
    .map((row, index) => ({ row, index }))
    .sort((p, q) => sign * compareCells(p.row[column], q.row[column]) || p.index - q.index)
    .map((p) => p.row);
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

function took(ms: number): string {
  return ms < 1 ? "under 1 ms" : `${ms} ms`;
}

/** summary is the sentence a screen reader hears when a run ends: "42 rows in 12 ms". */
export function summary(result: QueryResponse): string {
  if (result.affected !== undefined) {
    return `${plural(result.affected, "row", "rows")} affected in ${took(result.elapsedMs)}`;
  }
  if (result.columns.length === 0) {
    return `Done in ${took(result.elapsedMs)}`;
  }
  return `${plural(result.rows.length, "row", "rows")} in ${took(result.elapsedMs)}`;
}

/** The notice for a result the server cut, or null. */
export function limitNotice(result: QueryResponse): string | null {
  return result.truncated ? `The result is limited to ${result.rows.length} rows. Narrow the query to see the rest.` : null;
}

/** planText joins the rows of an EXPLAIN into text: the cells of a row side by side, one row per line. */
export function planText(result: QueryResponse): string {
  return result.rows.map((row) => row.map((c) => (c.n ? "NULL" : c.t)).join("  ")).join("\n");
}
