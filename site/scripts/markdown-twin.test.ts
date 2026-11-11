import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { navigation } from "../content/navigation.ts";

// The twins are build output, so this runs after `npm run build` and skips
// on a fresh checkout (the CI order is test, then build).
const docsOut = path.join(import.meta.dirname, "..", "out", "docs");
const built = fs.existsSync(path.join(docsOut, "introduction.md"));

test("every page of the current version has a Markdown twin", { skip: !built && "run npm run build first" }, () => {
  for (const cat of navigation) {
    for (const page of cat.pages) {
      const rel = path.join(cat.prefix ?? cat.dir, page === "" ? "" : page);
      const file = path.join(docsOut, `${rel}.md`);
      assert.ok(fs.existsSync(file), `missing ${path.relative(docsOut, file)}`);
    }
  }
});

test("a twin is plain Markdown", { skip: !built && "run npm run build first" }, () => {
  const text = fs.readFileSync(path.join(docsOut, "introduction.md"), "utf8");

  assert.ok(text.startsWith("# Introduction\n"), text.slice(0, 60));
  assert.ok(text.includes("Source: https://vukem.dev/docs/introduction/"));
  assert.ok(!/^import /m.test(text), "import lines must be dropped");
  assert.ok(!/<Steps>|<Card /.test(text), "JSX tags must be dropped");
});
