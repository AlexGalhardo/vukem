import { describe, expect, test } from "bun:test";
import { bracketMatch, commentEdit, findMatches, indentEdit, lineOf, newlineEdit, statementAt, visualColumn } from "./ops";
import type { Edit } from "./ops";
import { LineCache } from "./tokenizer";

function apply(text: string, e: Edit): string {
  return text.slice(0, e.from) + e.insert + text.slice(e.to);
}

describe("statementAt", () => {
  const text = "select 1;\nselect 'a;b' from t;\n\n-- note\nselect 3";

  test("takes the statement around the cursor", () => {
    const r = statementAt(text, 3);
    expect(text.slice(r?.start, r?.end)).toBe("select 1");
    const second = statementAt(text, text.indexOf("from"));
    expect(text.slice(second?.start, second?.end)).toBe("select 'a;b' from t");
  });

  test("does not split on a semicolon inside a string", () => {
    const r = statementAt(text, text.indexOf("a;b") + 1);
    expect(text.slice(r?.start, r?.end)).toBe("select 'a;b' from t");
  });

  test("a cursor right after the semicolon stays in that statement", () => {
    const r = statementAt(text, 9);
    expect(text.slice(r?.start, r?.end)).toBe("select 1");
  });

  test("a cursor in a blank stretch takes the statement before it", () => {
    const blank = "select 1;\n\n";
    const r = statementAt(blank, blank.length);
    expect(blank.slice(r?.start, r?.end)).toBe("select 1");
  });

  test("the last statement without a semicolon", () => {
    const r = statementAt(text, text.length);
    expect(text.slice(r?.start, r?.end)).toBe("-- note\nselect 3");
  });

  test("a comment before a statement goes with it", () => {
    const r = statementAt("-- only\nselect 1", 0);
    expect("-- only\nselect 1".slice(r?.start, r?.end)).toContain("select 1");
  });

  test("nothing but whitespace is null", () => {
    expect(statementAt("  \n ", 1)).toBeNull();
    expect(statementAt("", 0)).toBeNull();
  });
});

describe("indentEdit", () => {
  test("a caret inserts one unit", () => {
    expect(apply("ab", indentEdit("ab", 1, 1, false) as Edit)).toBe("a  b");
  });

  test("a selection indents every line it touches", () => {
    const e = indentEdit("a\nb\nc", 0, 3, false) as Edit;
    expect(apply("a\nb\nc", e)).toBe("  a\n  b\nc");
  });

  test("outdent removes a unit or a tab", () => {
    const e = indentEdit("  a\n\tb\n c", 0, 9, true) as Edit;
    expect(apply("  a\n\tb\n c", e)).toBe("a\nb\nc");
  });

  test("outdent of an unindented line changes nothing", () => {
    expect(indentEdit("a", 0, 1, true)).toBeNull();
  });
});

describe("commentEdit", () => {
  test("comments the lines of the selection", () => {
    const e = commentEdit("a\n  b\nc", 0, 5);
    expect(apply("a\n  b\nc", e)).toBe("-- a\n--   b\nc");
  });

  test("uncomments when every line is commented", () => {
    const t = "-- a\n  -- b";
    expect(apply(t, commentEdit(t, 0, t.length))).toBe("a\n  b");
  });

  test("a caret comments its own line", () => {
    expect(apply("select 1", commentEdit("select 1", 3, 3))).toBe("-- select 1");
  });

  test("a selection that ends at a line start leaves that line alone", () => {
    const t = "a\nb\n";
    expect(apply(t, commentEdit(t, 0, 2))).toBe("-- a\nb\n");
  });
});

describe("newlineEdit", () => {
  test("keeps the indentation", () => {
    const t = "  select 1";
    const e = newlineEdit(t, t.length, t.length);
    expect(apply(t, e)).toBe("  select 1\n  ");
    expect(e.selStart).toBe(t.length + 3);
  });
});

describe("findMatches", () => {
  test("finds every match, case-insensitively", () => {
    const m = findMatches("Select a\nselect b", "SELECT");
    expect(m.map((x) => [x.line, x.col, x.index])).toEqual([
      [0, 0, 0],
      [1, 0, 9],
    ]);
  });

  test("an empty query or one with a newline matches nothing", () => {
    expect(findMatches("abc", "")).toEqual([]);
    expect(findMatches("abc", "a\nb")).toEqual([]);
  });
});

describe("positions", () => {
  test("lineOf", () => {
    expect(lineOf("ab\ncd\nef", 4)).toEqual({ line: 1, col: 1 });
    expect(lineOf("ab", 2)).toEqual({ line: 0, col: 2 });
  });

  test("visualColumn expands tabs", () => {
    expect(visualColumn("\ta", 1)).toBe(4);
    expect(visualColumn("a\tb", 2)).toBe(4);
  });
});

describe("bracketMatch", () => {
  const lines = (text: string): ReturnType<LineCache["update"]> => new LineCache().update(text);

  test("pairs a bracket after the caret", () => {
    expect(bracketMatch(lines("f(a, (b))"), { line: 0, col: 1 })).toEqual([
      { line: 0, col: 1 },
      { line: 0, col: 8 },
    ]);
  });

  test("pairs a bracket before the caret, across lines", () => {
    expect(bracketMatch(lines("f(\n a\n)"), { line: 2, col: 1 })).toEqual([
      { line: 2, col: 0 },
      { line: 0, col: 1 },
    ]);
  });

  test("pairs braces, for JSON", () => {
    expect(bracketMatch(lines("{a: {b}}"), { line: 0, col: 1 })).toEqual([
      { line: 0, col: 0 },
      { line: 0, col: 7 },
    ]);
  });

  test("ignores brackets in strings and comments", () => {
    expect(bracketMatch(lines("f(')' /* ) */ )"), { line: 0, col: 1 })).toEqual([
      { line: 0, col: 1 },
      { line: 0, col: 14 },
    ]);
  });

  test("an unmatched bracket gives null", () => {
    expect(bracketMatch(lines("f(a"), { line: 0, col: 2 })).toBeNull();
    expect(bracketMatch(lines("select"), { line: 0, col: 2 })).toBeNull();
  });
});
