import assert from "node:assert/strict";
import { test } from "node:test";
import { docPageProblems } from "./check-last-updated.ts";

test("docPageProblems checks every page, including ones the PR did not touch", () => {
  const files = ["docs/fresh.mdx", "docs/deployment/releases-and-stability.mdx"];
  const entries = { "docs/fresh.mdx": "2026-10-30T10:00:00-03:00" };
  const dates = {
    "docs/fresh.mdx": "2026-10-30T13:00:00Z",
    "docs/deployment/releases-and-stability.mdx": "2026-10-29T23:32:04-03:00",
  };
  assert.deepEqual(docPageProblems(files, entries, dates), [
    "docs/deployment/releases-and-stability.mdx: missing from last-updated.json",
  ]);
});

test("docPageProblems flags a stale date, a dateless page and a deleted page", () => {
  const files = ["docs/old.mdx", "docs/undated.mdx"];
  const entries = {
    "docs/old.mdx": "2026-10-01T10:00:00-03:00",
    "docs/undated.mdx": "2026-10-01T10:00:00-03:00",
    "docs/gone.mdx": "2026-10-01T10:00:00-03:00",
    "notes.mdx": "2026-10-01T10:00:00-03:00",
  };
  const dates = { "docs/old.mdx": "2026-10-29T10:00:00-03:00" };
  assert.deepEqual(docPageProblems(files, entries, dates), [
    "docs/old.mdx: last-updated.json says 2026-10-01T10:00:00-03:00, last commit is 2026-10-29T10:00:00-03:00",
    "docs/undated.mdx: git has no commit date, so the page cannot show Last updated",
    "docs/gone.mdx: deleted, but still in last-updated.json",
  ]);
});

test("docPageProblems accepts a refreshed tree", () => {
  const files = ["docs/a.mdx", "versions/0.20/docs/a.mdx"];
  const entries = {
    "docs/a.mdx": "2026-10-30T10:00:00-03:00",
    "versions/0.20/docs/a.mdx": "2026-10-17T06:26:29-03:00",
  };
  const dates = {
    "docs/a.mdx": "2026-10-30T13:00:00Z",
    "versions/0.20/docs/a.mdx": "2026-10-17T06:26:29-03:00",
  };
  assert.deepEqual(docPageProblems(files, entries, dates), []);
});
