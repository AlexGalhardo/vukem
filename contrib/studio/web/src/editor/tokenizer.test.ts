import { describe, expect, test } from "bun:test";
import { LineCache, scanLine, startState, tokenize } from "./tokenizer";
import type { TokenKind } from "./tokenizer";

const letters: Record<string, TokenKind> = {
  k: "keyword",
  i: "identifier",
  s: "string",
  n: "number",
  c: "comment",
  o: "operator",
};

/** lex gives the tokens of a text as "k SELECT" strings, spaces left out. */
function lex(text: string): string[] {
  const letterOf = (kind: TokenKind): string => Object.keys(letters).find((l) => letters[l] === kind) ?? "?";
  return tokenize(text)
    .filter((t) => t.kind !== "space")
    .map((t) => `${letterOf(t.kind)} ${t.text}`);
}

const fixtures: [string, string[]][] = [
  ["select 1", ["k select", "n 1"]],
  ["SELECT a, b FROM t", ["k SELECT", "i a", "o ,", "i b", "k FROM", "i t"]],
  ["select 'it''s'", ["k select", "s 'it''s'"]],
  ["select ''", ["k select", "s ''"]],
  ["select '''' ", ["k select", "s ''''"]],
  [`select "a""b" from t`, ["k select", `i "a""b"`, "k from", "i t"]],
  ["select `a``b`", ["k select", "i `a``b`"]],
  ["-- note\nselect 1", ["c -- note", "k select", "n 1"]],
  ["select 1 -- tail", ["k select", "n 1", "c -- tail"]],
  ["/* a */ select", ["c /* a */", "k select"]],
  ["/* a\nb */ select", ["c /* a\nb */", "k select"]],
  ["select /* x */ 1", ["k select", "c /* x */", "n 1"]],
  ["select $$a'b$$", ["k select", "s $$a'b$$"]],
  ["select $tag$ x $$ y $tag$ from", ["k select", "s $tag$ x $$ y $tag$", "k from"]],
  ["select $$a\nb$$", ["k select", "s $$a\nb$$"]],
  ["select $1", ["k select", "i $1"]],
  ["select :name", ["k select", "i :name"]],
  ["select a::int", ["k select", "i a", "o ::", "i int"]],
  ["select 1.5", ["k select", "n 1.5"]],
  ["select .5", ["k select", "n .5"]],
  ["select 1e10, 2E-3", ["k select", "n 1e10", "o ,", "n 2E-3"]],
  ["select 0xFF", ["k select", "n 0xFF"]],
  ["select 10", ["k select", "n 10"]],
  ["a >= 1", ["i a", "o >=", "n 1"]],
  ["a <> b", ["i a", "o <>", "i b"]],
  ["a || b", ["i a", "o ||", "i b"]],
  ["a->>'x'", ["i a", "o ->>", "s 'x'"]],
  ["count(*)", ["i count", "o (", "o *", "o )"]],
  ["t.id", ["i t", "o .", "i id"]],
  ["a;b", ["i a", "o ;", "i b"]],
  ["select 1-- c", ["k select", "n 1", "c -- c"]],
  ["select 1/* c */", ["k select", "n 1", "c /* c */"]],
  ["select 'a' || 'b'", ["k select", "s 'a'", "o ||", "s 'b'"]],
  ["select 'a--b'", ["k select", "s 'a--b'"]],
  ["select '/* x */'", ["k select", "s '/* x */'"]],
  ["select '-- c' -- real", ["k select", "s '-- c'", "c -- real"]],
  ["select 'open", ["k select", "s 'open"]],
  ["select /* open", ["k select", "c /* open"]],
  ["Select Distinct x", ["k Select", "k Distinct", "i x"]],
  ["select naïve_col", ["k select", "i naïve_col"]],
  ["a.b.c", ["i a", "o .", "i b", "o .", "i c"]],
  ["select *", ["k select", "o *"]],
  ["x = ?", ["i x", "o =", "o ?"]],
  ["select [a]", ["k select", "o [", "i a", "o ]"]],
];

describe("tokenize", () => {
  for (const [text, want] of fixtures) {
    test(JSON.stringify(text), () => {
      expect(lex(text)).toEqual(want);
    });
  }

  test("there are at least 40 fixtures", () => {
    expect(fixtures.length >= 40).toBe(true);
  });

  test("the tokens join back to the text", () => {
    for (const [text] of fixtures) {
      expect(
        tokenize(text)
          .map((t) => t.text)
          .join(""),
      ).toBe(text);
    }
  });

  test("offsets point at the token in the text", () => {
    const text = "select 'a\nb' ,\n  x";
    for (const t of tokenize(text)) {
      expect(text.slice(t.start, t.start + t.text.length)).toBe(t.text);
    }
  });

  test("a blank line inside an open string stays in the string", () => {
    const text = "select 'a\n\nb' from t";
    expect(lex(text)).toEqual(["k select", "s 'a\n\nb'", "k from", "i t"]);
  });
});

describe("scanLine", () => {
  test("reports what is open at the end of a line", () => {
    expect(scanLine("select 'a", startState).end).toEqual({ open: "quote", quote: "'" });
    expect(scanLine("/* a", startState).end).toEqual({ open: "block" });
    expect(scanLine("$q$ a", startState).end).toEqual({ open: "dollar", tag: "$q$" });
    expect(scanLine("select 1", startState).end).toEqual({ open: "none" });
  });

  test("finishes an open comment and goes on", () => {
    const { tokens, end } = scanLine("b */ select", { open: "block" });
    expect(tokens.map((t) => t.kind)).toEqual(["comment", "space", "keyword"]);
    expect(tokens[0]?.continued).toBe(true);
    expect(end).toEqual({ open: "none" });
  });
});

describe("LineCache", () => {
  test("gives the same tokens as scanning every line", () => {
    const text = "select 'a\nb' from t\n/* c\nd */ where x";
    const cache = new LineCache();
    const lines = cache.update(text);
    expect(lines.map((l) => l.map((t) => t.text).join(""))).toEqual(text.split("\n"));
  });

  test("reuses the lines that did not change", () => {
    const cache = new LineCache();
    const before = cache.update("select 1\nselect 2\nselect 3");
    const after = cache.update("select 1\nselect 22\nselect 3");
    expect(after[0]).toBe(before[0] as readonly unknown[]);
    expect(after[2]).toBe(before[2] as readonly unknown[]);
    expect(after[1]).not.toBe(before[1] as readonly unknown[]);
  });

  test("opening a quote redraws the lines below it", () => {
    const cache = new LineCache();
    const before = cache.update("select 1\nselect 2");
    const after = cache.update("select '\nselect 2");
    expect(after[1]).not.toBe(before[1] as readonly unknown[]);
    expect(after[1]?.[0]?.kind).toBe("string");
  });
});
