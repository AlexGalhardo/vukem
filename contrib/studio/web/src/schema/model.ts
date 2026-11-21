import type { SchemaTable } from "../api/types";
import type { LayoutInput } from "./layout";

/** One foreign key as a row of the relationships table: both ends. */
export interface Relation {
  fromTable: string;
  fromColumns: string[];
  toTable: string;
  toColumns: string[];
}

/** relations lists every foreign key of the schema, ordered by the table that holds it and then by its target. */
export function relations(tables: readonly SchemaTable[]): Relation[] {
  const out: Relation[] = [];
  for (const t of tables) {
    for (const fk of t.foreignKeys) {
      out.push({ fromTable: t.name, fromColumns: fk.columns, toTable: fk.refTable, toColumns: fk.refColumns });
    }
  }
  return out.sort(
    (a, b) =>
      a.fromTable.localeCompare(b.fromTable) ||
      a.toTable.localeCompare(b.toTable) ||
      a.fromColumns.join(",").localeCompare(b.fromColumns.join(",")),
  );
}

/** referencedBy lists the foreign keys of other tables (and of the table itself) that point at name. */
export function referencedBy(all: readonly Relation[], name: string): Relation[] {
  return all.filter((r) => r.toTable === name);
}

/** layoutInputs is what the layout needs from the schema: names and the tables each one points at. */
export function layoutInputs(tables: readonly SchemaTable[]): LayoutInput[] {
  return tables.map((t) => ({ name: t.name, refs: t.foreignKeys.map((fk) => fk.refTable) }));
}

/** matchesFilter is the table list's filter box: a case-insensitive substring of the name. */
export function matchesFilter(name: string, filter: string): boolean {
  const f = filter.trim().toLowerCase();
  return f === "" || name.toLowerCase().includes(f);
}

export type KeyRole = "primary" | "foreign" | "unique";

/** keyRoles says which key badges a column gets: primary key, foreign key and a single-column unique index. */
export function keyRoles(table: SchemaTable, column: string): KeyRole[] {
  const roles: KeyRole[] = [];
  if (table.primaryKey.includes(column)) {
    roles.push("primary");
  }
  if (table.foreignKeys.some((fk) => fk.columns.includes(column))) {
    roles.push("foreign");
  }
  if (table.indexes.some((ix) => !ix.primary && ix.unique && ix.columns.length === 1 && ix.columns[0] === column)) {
    roles.push("unique");
  }
  return roles;
}

/** The diagram draws at most this many tables, so its DOM stays small. */
export const maxDiagramTables = 300;

/**
 * diagramTables picks what the diagram draws: every table, or with focus only
 * the selected one and the tables joined to it; never more than maxDiagramTables.
 */
export function diagramTables(
  tables: readonly SchemaTable[],
  selected: string | null,
  focusOnly: boolean,
): { tables: SchemaTable[]; cut: boolean } {
  let list = [...tables];
  if (focusOnly && selected !== null) {
    const near = new Set<string>([selected]);
    for (const t of tables) {
      for (const fk of t.foreignKeys) {
        if (t.name === selected) near.add(fk.refTable);
        if (fk.refTable === selected) near.add(t.name);
      }
    }
    list = list.filter((t) => near.has(t.name));
  }
  const cut = list.length > maxDiagramTables;
  return { tables: cut ? list.slice(0, maxDiagramTables) : list, cut };
}
