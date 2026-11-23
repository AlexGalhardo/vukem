import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import config from "../astro.config.mjs";

const siteDir = path.join(import.meta.dirname, "..");

test("the old finance guide address redirects to the SaaS edition page", () => {
  assert.equal(config.redirects?.["/docs/finance-saas"], "/docs/finance-saas-edition/");
});

test("both finance pages exist and the old page is gone", () => {
  const dir = path.join(siteDir, "content", "docs", "getting-started");
  assert.ok(fs.existsSync(path.join(dir, "finance-saas-edition.mdx")));
  assert.ok(fs.existsSync(path.join(dir, "finance-personal-app.mdx")));
  assert.ok(!fs.existsSync(path.join(dir, "finance-saas.mdx")));
});

// Runs after `npm run build`; skipped on a clean checkout, where out/ does not exist.
test("the built old address is a refresh to the new page", { skip: !fs.existsSync(path.join(siteDir, "out", "docs")) }, () => {
  const html = fs.readFileSync(path.join(siteDir, "out", "docs", "finance-saas", "index.html"), "utf8");
  assert.match(html, /http-equiv="refresh"[^>]*\/docs\/finance-saas-edition\//);
});
