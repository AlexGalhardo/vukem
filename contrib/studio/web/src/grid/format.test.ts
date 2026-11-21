import { describe, expect, test } from "bun:test";
import type { ApiCell, Column } from "../api/types";
import { cellView, columnWidth, jsonCompact, jsonPretty, jsonValue, plainText, toCSV, toJSON, toTSV } from "./format";

const columns: Column[] = [
  { name: "id", type: "integer", nullable: false },
  { name: "note", type: "text", nullable: true },
  { name: "ok", type: "boolean", nullable: true },
  { name: "meta", type: "json", nullable: true },
];

const rows: ApiCell[][] = [
  [{ t: "1", l: true }, { t: "a\tb\nc" }, { t: "true", l: true }, { t: '{"a": [1, 2]}' }],
  [{ t: "2", l: true }, { t: "", n: true }, { t: "false", l: true }, { t: "", n: true }],
  [{ t: "9007199254740993", l: true }, { t: 'say "hi", ok' }, { t: "1", l: true }, { t: "{broken" }],
];

describe("cellView", () => {
  test("NULL is its own token, distinct from the text NULL", () => {
    expect(cellView({ t: "", n: true }, "text")).toEqual({ tone: "null", text: "NULL" });
    expect(cellView({ t: "NULL" }, "text")).toEqual({ tone: "text", text: "NULL" });
  });

  test("numbers, booleans, dates, json and binary", () => {
    expect(cellView({ t: "42", l: true }, "number").tone).toBe("number");
    expect(cellView({ t: "1", l: true }, "boolean").text).toBe("true");
    expect(cellView({ t: "0", l: true }, "boolean").text).toBe("false");
    expect(cellView({ t: "2026-01-02T00:00:00Z" }, "date").tone).toBe("date");
    expect(cellView({ t: '{ "a": 1 }' }, "json").text).toBe('{"a":1}');
    expect(cellView({ t: "<binary 2 bytes>", b: true }, "binary")).toEqual({ tone: "binary", text: "<binary 2 bytes>" });
  });

  test("a cut JSON value is shown as it came", () => {
    expect(cellView({ t: '{"a":', x: true }, "json").text).toBe('{"a":');
  });
});

describe("json", () => {
  test("compact and pretty", () => {
    expect(jsonCompact('{\n "a": 1\n}')).toBe('{"a":1}');
    expect(jsonCompact("nope")).toBe("nope");
    expect(jsonPretty('{"a":1}')).toBe('{\n  "a": 1\n}');
    expect(jsonPretty("nope")).toBeNull();
  });
});

describe("copy", () => {
  test("plain text of NULL is empty", () => {
    expect(plainText({ t: "", n: true })).toBe("");
    expect(plainText({ t: "x" })).toBe("x");
  });

  test("tab-separated text flattens tabs and newlines", () => {
    expect(toTSV(columns.slice(0, 2), rows.slice(0, 2).map((r) => r.slice(0, 2)))).toBe("id\tnote\n1\ta b c\n2\t");
  });

  test("CSV quotes what needs it and uses CRLF", () => {
    expect(toCSV(columns.slice(0, 2), rows.slice(1).map((r) => r.slice(0, 2)))).toBe('id,note\r\n2,\r\n9007199254740993,"say ""hi"", ok"');
  });

  test("JSON keeps types, NULL and parsed JSON, and keeps big integers as text", () => {
    const parsed = JSON.parse(toJSON(columns, rows)) as Record<string, unknown>[];
    expect(parsed[0]).toEqual({ id: 1, note: "a\tb\nc", ok: true, meta: { a: [1, 2] } });
    expect(parsed[1]).toEqual({ id: 2, note: null, ok: false, meta: null });
    expect(parsed[2]?.id).toBe("9007199254740993");
    expect(parsed[2]?.meta).toBe("{broken");
    expect(jsonValue({ t: "1.5", l: true }, "number")).toBe(1.5);
    expect(jsonValue({ t: "0", l: true }, "boolean")).toBe(false);
    expect(jsonValue({ t: "1", l: true }, "boolean")).toBe(true);
    expect(jsonValue({ t: "1", l: true }, "number")).toBe(1);
  });
});

describe("columnWidth", () => {
  test("is by kind and grows for a long name", () => {
    expect(columnWidth({ name: "id", type: "integer", nullable: false })).toBe(120);
    expect(columnWidth({ name: "title", type: "text", nullable: false })).toBe(220);
    expect(columnWidth({ name: "a_very_long_column_name_indeed", type: "integer", nullable: false })).toBe(304);
    expect(columnWidth({ name: "x".repeat(200), type: "integer", nullable: false })).toBe(360);
  });
});
