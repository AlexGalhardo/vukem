import { describe, expect, test } from "bun:test";
import type { SchemaTable } from "../api/types";
import { diagramTables, keyRoles, layoutInputs, matchesFilter, maxDiagramTables, referencedBy, relations } from "./model";

const col = (name: string): SchemaTable["columns"][number] => ({ name, type: "integer", nullable: false });

const tables: SchemaTable[] = [
  {
    name: "pets",
    columns: [col("id"), col("owner_id"), col("tag")],
    primaryKey: ["id"],
    foreignKeys: [{ columns: ["owner_id"], refTable: "owners", refColumns: ["id"] }],
    indexes: [
      { name: "", columns: ["id"], unique: true, primary: true },
      { name: "pets_tag", columns: ["tag"], unique: true, primary: false },
    ],
  },
  { name: "owners", columns: [col("id")], primaryKey: ["id"], foreignKeys: [], indexes: [] },
  { name: "logs", columns: [col("line")], primaryKey: [], foreignKeys: [], indexes: [] },
];

describe("schema model", () => {
  test("relations lists every foreign key with both ends, in order", () => {
    expect(relations(tables)).toEqual([{ fromTable: "pets", fromColumns: ["owner_id"], toTable: "owners", toColumns: ["id"] }]);
  });

  test("referencedBy finds who points at a table", () => {
    expect(referencedBy(relations(tables), "owners").map((r) => r.fromTable)).toEqual(["pets"]);
    expect(referencedBy(relations(tables), "pets")).toEqual([]);
  });

  test("layoutInputs carries names and targets", () => {
    expect(layoutInputs(tables)[0]).toEqual({ name: "pets", refs: ["owners"] });
  });

  test("the filter is a case-insensitive substring", () => {
    expect(matchesFilter("Owners", "own")).toBe(true);
    expect(matchesFilter("owners", "  PET ")).toBe(false);
    expect(matchesFilter("owners", "")).toBe(true);
  });

  test("key roles: primary, foreign and a single-column unique index", () => {
    const pets = tables[0] as SchemaTable;
    expect(keyRoles(pets, "id")).toEqual(["primary"]);
    expect(keyRoles(pets, "owner_id")).toEqual(["foreign"]);
    expect(keyRoles(pets, "tag")).toEqual(["unique"]);
  });

  test("the diagram can focus on a table and its neighbours", () => {
    expect(diagramTables(tables, "owners", true).tables.map((t) => t.name).sort()).toEqual(["owners", "pets"]);
    expect(diagramTables(tables, null, true).tables).toHaveLength(3);
  });

  test("the diagram never draws more than the limit", () => {
    const many: SchemaTable[] = Array.from({ length: maxDiagramTables + 5 }, (_, i) => ({
      name: `t${i}`,
      columns: [],
      primaryKey: [],
      foreignKeys: [],
      indexes: [],
    }));
    const r = diagramTables(many, null, false);
    expect(r.tables).toHaveLength(maxDiagramTables);
    expect(r.cut).toBe(true);
  });
});
