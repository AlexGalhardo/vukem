import type { ApiCell, Column, ForeignKey, Reference, TableSummary } from "../api/types";
import { viewToSearch } from "./filters";
import type { Filter } from "./filters";
import { packKey } from "./rowKey";

/** A link from a row to another table: the table and the query string that shows the rows. */
export interface RowLink {
  label: string;
  table: string;
  search: string;
}

/** valuesOf reads the text of the named columns of a row, or null when one is NULL or missing. */
function valuesOf(columns: readonly Column[], cells: readonly ApiCell[], names: readonly string[]): string[] | null {
  const out: string[] = [];
  for (const name of names) {
    const cell = cells[columns.findIndex((c) => c.name === name)];
    if (!cell || cell.n) {
      return null;
    }
    out.push(cell.t);
  }
  return out;
}

function eqFilters(columns: readonly string[], values: readonly string[]): Filter[] {
  return columns.map((column, i) => ({ column, op: "eq", value: values[i] ?? "" }));
}

/**
 * foreignKeyLink opens the row a foreign key points at. When the key columns
 * are the primary key of the other table the row's drawer opens as well.
 */
export function foreignKeyLink(
  fk: ForeignKey,
  columns: readonly Column[],
  cells: readonly ApiCell[],
  tables: readonly TableSummary[],
): RowLink | null {
  const values = valuesOf(columns, cells, fk.columns);
  if (!values) {
    return null;
  }
  const target = tables.find((t) => t.name === fk.refTable);
  const isKey =
    target !== undefined &&
    target.primaryKey.length === fk.refColumns.length &&
    target.primaryKey.every((c, i) => c === fk.refColumns[i]);

  return {
    label: `Open the row of ${fk.refTable} that ${fk.columns.join(", ")} points at`,
    table: fk.refTable,
    search: viewToSearch(
      { filters: eqFilters(fk.refColumns, values), sort: null, desc: false },
      isKey ? packKey(values) : null,
    ),
  };
}

/** referenceLink lists the rows of another table that point at this row. */
export function referenceLink(ref: Reference, columns: readonly Column[], cells: readonly ApiCell[]): RowLink | null {
  const values = valuesOf(columns, cells, ref.refColumns);
  if (!values) {
    return null;
  }
  return {
    label: `${ref.table} (${ref.columns.join(", ")})`,
    table: ref.table,
    search: viewToSearch({ filters: eqFilters(ref.columns, values), sort: null, desc: false }),
  };
}
