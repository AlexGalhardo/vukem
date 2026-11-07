import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { readCommittedDates } from "../lib/committed-dates.ts";

test("readCommittedDates treats a missing file as no dates", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "vukem-dates-"));
  assert.deepEqual(readCommittedDates(path.join(dir, "last-updated.json")), {});
});

test("readCommittedDates keeps string entries and drops the rest", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "vukem-dates-"));
  const file = path.join(dir, "last-updated.json");
  writeFileSync(file, JSON.stringify({ "docs/a.mdx": "2026-11-06T00:00:00Z", n: 1 }));
  assert.deepEqual(readCommittedDates(file), { "docs/a.mdx": "2026-11-06T00:00:00Z" });
});

test("readCommittedDates rejects invalid JSON and a list", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "vukem-dates-"));
  const bad = path.join(dir, "bad.json");
  writeFileSync(bad, "{");
  assert.deepEqual(readCommittedDates(bad), {});

  const list = path.join(dir, "list.json");
  writeFileSync(list, "[]");
  assert.deepEqual(readCommittedDates(list), {});
});
