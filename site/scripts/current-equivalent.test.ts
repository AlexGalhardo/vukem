import assert from "node:assert/strict";
import fs from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import path from "node:path";
import { test } from "node:test";

// lib/content.ts imports through the "@/" alias, which node cannot resolve,
// so the function is cut out of the source and run against stubs.
const source = fs.readFileSync(path.join(import.meta.dirname, "..", "lib", "content.ts"), "utf8");
const start = source.indexOf("export function currentEquivalent");
const body = source.slice(start, source.indexOf("\n}\n", start) + 2).replace("export ", "");

type D = { slug: string[]; version: { id: string; prefix: string } };
const current = { id: "1.0", prefix: "" };
const old = { id: "0.20", prefix: "0.20" };
const currentDocs: D[] = [{ slug: ["basics", "routing"], version: current }];
const currentEquivalent = new Function("currentVersion", "getAllDocs", `${stripTypeScriptTypes(body)}; return currentEquivalent;`)(current, () => currentDocs) as (d: D) => D | undefined;

test("an old-version page maps to the current page with the same path", () => {
  assert.equal(currentEquivalent({ slug: ["0.20", "basics", "routing"], version: old }), currentDocs[0]);
});

test("an old-version page without a current counterpart has no equivalent", () => {
  assert.equal(currentEquivalent({ slug: ["0.20", "basics", "removed-page"], version: old }), undefined);
});

test("a current-version page has no equivalent (it is its own canonical)", () => {
  assert.equal(currentEquivalent(currentDocs[0]), undefined);
});
