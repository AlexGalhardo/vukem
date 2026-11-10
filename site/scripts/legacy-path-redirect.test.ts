import assert from "node:assert/strict";
import fs from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import path from "node:path";
import { test } from "node:test";

// The component is a .tsx file, which node cannot import, so the pure
// function is cut out of the source and run as is.
const source = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "legacy-path-redirect.tsx"), "utf8");
const start = source.indexOf("export function legacyTarget");
const body = source.slice(start, source.indexOf("\n}\n", start) + 2).replace("export ", "");
const legacyTarget = new Function(`${stripTypeScriptTypes(body)}; return legacyTarget;`)() as (l: {
  origin: string;
  pathname: string;
  search: string;
  hash: string;
}) => string | null;

const at = (pathname: string, search = "", hash = "") => legacyTarget({ origin: "https://vukem.dev", pathname, search, hash });

test("old /pt-br paths go to the same path on this origin", () => {
  assert.equal(at("/pt-br"), "/");
  assert.equal(at("/pt-br/"), "/");
  assert.equal(at("/pt-br/docs/mail", "?a=1", "#x"), "/docs/mail?a=1#x");
});

test("other paths are left alone", () => {
  assert.equal(at("/docs/mail"), null);
  assert.equal(at("/pt-brasil"), null);
});

test("a protocol-relative or backslash remainder never leaves the origin", () => {
  for (const p of ["/pt-br//evil.com", "/pt-br///evil.com/x", "/pt-br/\evil.com", "/pt-br/\/evil.com", "/pt-br/%2F%2Fevil.com"]) {
    const t = at(p);
    assert.ok(t !== null && t.startsWith("/") && !t.startsWith("//") && !t.startsWith("/\\"), `${p} -> ${t}`);
    assert.equal(new URL(t, "https://vukem.dev").origin, "https://vukem.dev", p);
  }
  assert.equal(at("/pt-br//evil.com"), "/evil.com");
});

test("the inline script on the 404 page applies the same-origin check", () => {
  const page = fs.readFileSync(path.join(import.meta.dirname, "..", "src", "pages", "404.astro"), "utf8");
  assert.ok(page.includes(String.raw`replace(/^[\/\\]+/`), "404.astro must strip leading slashes and backslashes");
  assert.ok(page.includes("url.origin !== location.origin"), "404.astro must check the origin");
});
