import { describe, expect, test } from "bun:test";
import { checkConsole, findKey, jsonTokens, JsonLineCache, parseJSON, template } from "./json";

describe("parseJSON", () => {
  test("reads every JSON type", () => {
    const r = parseJSON('{"a": [1, -2.5e3, "x\\n\\u0041", true, false, null], "b": {}}');
    expect(r.ok).toBe(true);
    expect(r.ok && r.value).toEqual({ a: [1, -2500, "x\nA", true, false, null], b: {} });
  });

  test("reads Extended JSON wrappers as objects", () => {
    const r = parseJSON('{"_id": {"$oid": "65a1b2c3d4e5f60718293a4b"}, "at": {"$date": "2026-01-01T00:00:00Z"}}');
    expect(r.ok).toBe(true);
  });

  test("says where it broke, by line and column", () => {
    const r = parseJSON('{\n  "a": 1,\n  "b": ]\n}');
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect([r.error.line, r.error.column]).toEqual([3, 8]);
      expect(r.error.message).toContain("value was expected");
    }
  });

  test("refuses what JSON refuses", () => {
    for (const text of ["{'a': 1}", '{"a": 1,}', "[1,]", '{"a" 1}', "01", "1.", "NaN", '"open', '{"a": 1} x', "", "[1 2]", '"\\x"', "{a: 1}"]) {
      expect(parseJSON(text).ok).toBe(false);
    }
  });

  test("a deep nest is refused, not a stack overflow", () => {
    expect(parseJSON("[".repeat(200) + "]".repeat(200)).ok).toBe(false);
  });

  test("a __proto__ key stays a plain field", () => {
    const r = parseJSON('{"__proto__": {"x": 1}}');
    expect(r.ok && Object.keys(r.value as object)).toEqual(["__proto__"]);
    expect(({} as { x?: number }).x).toBeUndefined();
  });
});

describe("checkConsole", () => {
  test("a find and an aggregate become the request", () => {
    const find = checkConsole('{"collection": "users", "filter": {"age": {"$gt": 3}}}', false);
    expect(find).toEqual({ ok: true, request: { kind: "find", collection: "users", filter: '{"age":{"$gt":3}}' } });
    const agg = checkConsole('{"collection": "users", "pipeline": [{"$match": {}}, {"$limit": 2}]}', false);
    expect(agg).toEqual({ ok: true, request: { kind: "aggregate", collection: "users", pipeline: '[{"$match":{}},{"$limit":2}]' } });
  });

  test("the template is a valid find", () => {
    expect(checkConsole(template("users"), false).ok).toBe(true);
  });

  test("refuses the shapes it does not know", () => {
    const bad = [
      "[]",
      "1",
      '{"filter": {}}',
      '{"collection": 1, "filter": {}}',
      '{"collection": "u"}',
      '{"collection": "u", "filter": {}, "pipeline": []}',
      '{"collection": "u", "filter": []}',
      '{"collection": "u", "pipeline": {}}',
      '{"collection": "u", "pipeline": [1]}',
      '{"collection": "u", "filter": {}, "limit": 1}',
    ];
    for (const text of bad) {
      expect(checkConsole(text, false).ok).toBe(false);
    }
  });

  test("refuses the operators that run JavaScript, at any depth, with or without --write", () => {
    const bodies = [
      '{"collection": "u", "filter": {"$where": "sleep(1000)"}}',
      '{"collection": "u", "filter": {"a": {"$function": {"body": "x", "args": [], "lang": "js"}}}}',
      '{"collection": "u", "filter": {"$and": [{"b": 1}, {"$where": "x"}]}}',
      '{"collection": "u", "pipeline": [{"$group": {"_id": 1, "x": {"$accumulator": {}}}}]}',
      '{"collection": "u", "pipeline": [{"$match": {"$expr": {"$function": {}}}}]}',
    ];
    for (const write of [false, true]) {
      for (const body of bodies) {
        const r = checkConsole(body, write);
        expect(r.ok).toBe(false);
        expect(!r.ok && r.message).toContain("JavaScript");
      }
    }
  });

  test("$out and $merge need --write", () => {
    const out = '{"collection": "u", "pipeline": [{"$match": {}}, {"$out": "x"}]}';
    const merge = '{"collection": "u", "pipeline": [{"$merge": {"into": "x"}}]}';
    expect(checkConsole(out, false).ok).toBe(false);
    expect(checkConsole(merge, false).ok).toBe(false);
    expect(checkConsole(out, true).ok).toBe(true);
    expect(checkConsole(merge, true).ok).toBe(true);
  });

  test("a syntax error reports its position", () => {
    const r = checkConsole('{"collection": "u",\n "filter": {]}', false);
    expect(r.ok).toBe(false);
    expect(!r.ok && r.line).toBe(2);
  });
});

describe("findKey", () => {
  test("finds a key anywhere and nothing otherwise", () => {
    expect(findKey({ a: [{ b: { $out: 1 } }] }, ["$out"])).toBe("$out");
    expect(findKey({ a: "$out" }, ["$out"])).toBeNull();
  });
});

describe("editor tokens", () => {
  test("keys, strings, numbers, words and brackets get the kinds the editor colors", () => {
    const toks = jsonTokens('  {"a": "b", "n": -1.5e2, "t": true},');
    const kinds = toks.filter((t) => t.kind !== "space").map((t) => `${t.kind}:${t.text}`);
    expect(kinds).toEqual([
      "operator:{",
      'identifier:"a"',
      "operator::",
      'string:"b"',
      "operator:,",
      'identifier:"n"',
      "operator::",
      "number:-1.5e2",
      "operator:,",
      'identifier:"t"',
      "operator::",
      "keyword:true",
      "operator:}",
      "operator:,",
    ]);
    expect(toks.map((t) => t.text).join("")).toBe('  {"a": "b", "n": -1.5e2, "t": true},');
  });

  test("an unclosed string ends at the line and the tokens still join to the text", () => {
    const line = '{"a": "open';
    expect(jsonTokens(line).map((t) => t.text).join("")).toBe(line);
  });

  test("the line cache reuses a line that did not change", () => {
    const cache = new JsonLineCache();
    const a = cache.update('{\n  "a": 1\n}');
    const b = cache.update('{\n  "a": 1,\n  "b": 2\n}');
    expect(a[0]).toBe(b[0]);
    expect(b).toHaveLength(4);
  });
});
