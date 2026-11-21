import { describe, expect, test } from "bun:test";
import { completeJson } from "./completion";
import { canUseField, cellOf, confirmId, inferColumns, parseDoc, rowOf } from "./docs";

describe("documents as rows", () => {
  test("columns come from the first documents, _id first", () => {
    const docs = [parseDoc('{"name": "a", "_id": 1}'), parseDoc('{"name": "b", "age": 3}'), null];
    expect(inferColumns(docs)).toEqual(["_id", "name", "age"]);
  });

  test("only the first 100 documents are read", () => {
    const docs = Array.from({ length: 101 }, (_, i) => parseDoc(i === 100 ? '{"late": 1}' : '{"a": 1}'));
    expect(inferColumns(docs)).toEqual(["a"]);
  });

  test("cells: NULL, text, literals, wrappers and nested values", () => {
    expect(cellOf(undefined)).toEqual({ t: "", n: true });
    expect(cellOf(null)).toEqual({ t: "", n: true });
    expect(cellOf("x")).toEqual({ t: "x" });
    expect(cellOf(5)).toEqual({ t: "5", l: true });
    expect(cellOf(false)).toEqual({ t: "false", l: true });
    expect(cellOf({ $oid: "abc" })).toEqual({ t: "abc" });
    expect(cellOf({ $date: "2026-01-01T00:00:00Z" })).toEqual({ t: "2026-01-01T00:00:00Z" });
    expect(cellOf({ a: [1, { b: 2 }] })).toEqual({ t: '{"a":[1,{"b":2}]}' });
    expect(cellOf("y".repeat(400)).x).toBe(true);
  });

  test("a row follows the columns and leaves a missing field NULL", () => {
    expect(rowOf(["a", "b"], parseDoc('{"a": 1}'))).toEqual([{ t: "1", l: true }, { t: "", n: true }]);
  });

  test("a document that is not an object is null", () => {
    expect(parseDoc("[1]")).toBeNull();
    expect(parseDoc("nope")).toBeNull();
  });

  test("field paths follow the server rule", () => {
    for (const ok of ["a", "a.b", "_x.y1.2"]) expect(canUseField(ok)).toBe(true);
    for (const bad of ["", "$where", "a b", "a..b", ".a", "a."]) expect(canUseField(bad)).toBe(false);
  });

  test("the confirmation is what the server compares", () => {
    expect(confirmId('{"$oid":"65a1b2c3d4e5f60718293a4b"}')).toBe("65a1b2c3d4e5f60718293a4b");
    expect(confirmId('{"$numberInt":"7"}')).toBe("7");
    expect(confirmId('{"$numberLong":"9"}')).toBe("9");
    expect(confirmId('"abc"')).toBe("abc");
    expect(confirmId('{"a":1}')).toBe('{"a":1}');
  });
});

describe("console completion", () => {
  const collections = ["users", "orders", "user_logs"];

  test("a $-word completes to stages and operators, never to a script operator", () => {
    const text = '{"collection": "users", "pipeline": [{"$ma';
    const r = completeJson(text, text.length, collections, false);
    expect(r.items.map((i) => i.label)).toContain("$match");
    const w = '{"collection": "users", "filter": {"$w';
    expect(completeJson(w, w.length, collections, false).items).toEqual([]);
    const f = '{"filter": {"$fu';
    expect(completeJson(f, f.length, collections, true).items).toEqual([]);
  });

  test("the collection value completes to collection names", () => {
    const text = '{"collection": "us';
    const r = completeJson(text, text.length, collections, false);
    expect(r.items.map((i) => i.label)).toEqual(["users", "user_logs"]);
    expect(r.from).toBe(text.length - 2);
  });

  test("the from of a lookup completes to collection names too", () => {
    const text = '{"collection": "users", "pipeline": [{"$lookup": {"from": "or';
    expect(completeJson(text, text.length, collections, false).items.map((i) => i.label)).toEqual(["orders"]);
  });

  test("outside a string there is nothing to complete", () => {
    const text = '{"collection": "users", "filter": ';
    expect(completeJson(text, text.length, collections, true).items).toEqual([]);
  });

  test("a top-level field name completes", () => {
    const text = '{"coll';
    expect(completeJson(text, text.length, collections, false).items.map((i) => i.label)).toEqual(["collection"]);
  });
});
