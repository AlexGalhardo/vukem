import { describe, expect, test } from "bun:test";
import type { ApiCell, QueryResponse } from "../api/types";
import { limitNotice, planText, resultColumns, sortRows, staticGridData, summary } from "./resultData";

const cell = (t: string): ApiCell => ({ t });
const nul: ApiCell = { t: "NULL", n: true };

function result(over: Partial<QueryResponse>): QueryResponse {
  return { columns: ["a"], rows: [], truncated: false, elapsedMs: 12, kind: "SELECT", ...over };
}

describe("summary", () => {
  test("rows and time", () => {
    expect(summary(result({ rows: Array.from({ length: 42 }, () => [cell("x")]) }))).toBe("42 rows in 12 ms");
    expect(summary(result({ rows: [[cell("x")]] }))).toBe("1 row in 12 ms");
    expect(summary(result({ elapsedMs: 0 }))).toBe("0 rows in under 1 ms");
  });

  test("a write says how many rows it changed", () => {
    expect(summary(result({ columns: [], affected: 3 }))).toBe("3 rows affected in 12 ms");
    expect(summary(result({ columns: [], affected: 1 }))).toBe("1 row affected in 12 ms");
  });

  test("a statement with no rows and no count is done", () => {
    expect(summary(result({ columns: [] }))).toBe("Done in 12 ms");
  });
});

describe("limitNotice", () => {
  test("says how many rows the limit left", () => {
    const rows = Array.from({ length: 1000 }, () => [cell("x")]);
    expect(limitNotice(result({ rows, truncated: true }))).toContain("limited to 1000 rows");
    expect(limitNotice(result({ rows }))).toBeNull();
  });
});

describe("sortRows", () => {
  const rows = [[cell("10")], [cell("9")], [nul], [cell("a")]];

  test("sorts numbers as numbers, text after, NULL first", () => {
    expect(sortRows(rows, 0, false).map((r) => r[0]?.t)).toEqual(["NULL", "9", "10", "a"]);
    expect(sortRows(rows, 0, true).map((r) => r[0]?.t)).toEqual(["a", "10", "9", "NULL"]);
  });

  test("keeps the order of equal rows and leaves the input alone", () => {
    const same = [[cell("1"), cell("x")], [cell("1"), cell("y")]];
    expect(sortRows(same, 0, true).map((r) => r[1]?.t)).toEqual(["x", "y"]);
    expect(sortRows(rows, null, false)).toBe(rows);
    expect(rows[0]?.[0]?.t).toBe("10");
  });
});

describe("planText", () => {
  test("one row per line", () => {
    expect(planText(result({ rows: [[cell("2"), cell("SCAN users")], [cell("3"), nul]] }))).toBe("2  SCAN users\n3  NULL");
  });
});

describe("staticGridData", () => {
  test("serves the rows it was given and never pages", () => {
    const rows = [[cell("a")], [cell("b")]];
    const data = staticGridData(rows, 1);
    expect(data.rowCount).toBe(2);
    expect(data.row(1)).toEqual([cell("b")]);
    expect(data.row(2)).toBeUndefined();
    expect(data.exact).toBe(2);
    expect(data.loading).toBe(false);
  });

  test("columns read as text", () => {
    expect(resultColumns(["a", "b"])).toEqual([
      { name: "a", type: "", nullable: true },
      { name: "b", type: "", nullable: true },
    ]);
  });
});
