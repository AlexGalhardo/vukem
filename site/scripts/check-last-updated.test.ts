import assert from "node:assert/strict";
import { test } from "node:test";
import { staleEntries } from "./check-last-updated.ts";

const exists = (f: string): boolean => f !== "docs/gone.mdx";

test("staleEntries", () => {
  const entries = {
    "docs/fresh.mdx": "2026-10-30T10:00:00-03:00",
    "docs/old.mdx": "2026-10-01T10:00:00-03:00",
    "docs/gone.mdx": "2026-10-01T10:00:00-03:00",
  };
  const dates = {
    // Same instant in another zone is not stale.
    "docs/fresh.mdx": "2026-10-30T13:00:00Z",
    "docs/old.mdx": "2026-10-29T10:00:00-03:00",
    "docs/new.mdx": "2026-10-30T10:00:00-03:00",
  };
  const changed = [
    "docs/fresh.mdx",
    "docs/old.mdx",
    "docs/new.mdx",
    "docs/gone.mdx",
    "navigation.ts",
    "docs/image.png",
  ];

  assert.deepEqual(staleEntries(changed, entries, dates, exists), [
    "docs/old.mdx: last-updated.json says 2026-10-01T10:00:00-03:00, last commit is 2026-10-29T10:00:00-03:00",
    "docs/new.mdx: missing from last-updated.json",
    "docs/gone.mdx: deleted, but still in last-updated.json",
  ]);
});

test("staleEntries accepts a refreshed branch", () => {
  const entries = { "docs/a.mdx": "2026-10-30T10:00:00-03:00" };
  assert.deepEqual(staleEntries(["docs/a.mdx"], entries, { "docs/a.mdx": "2026-10-30T10:00:00-03:00" }, exists), []);
});
