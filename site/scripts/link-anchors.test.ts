import assert from "node:assert/strict";
import { test } from "node:test";
import { checkFragment, pageAnchors } from "./link-anchors.ts";

const body = ["## Pick one", "", "### `APP_KEY` without the global CLI", "", '<details id="marketplace">', "", "```md", "## Not a heading", "```"].join("\n");

test("anchors come from headings and explicit ids, not code fences", () => {
  const a = pageAnchors(body);
  assert.ok(a.has("pick-one") && a.has("app_key-without-the-global-cli") && a.has("marketplace"));
  assert.ok(!a.has("not-a-heading"));
});

test("a fragment with no matching anchor is refused", () => {
  const a = pageAnchors(body);
  assert.match(checkFragment("/docs/showcase#nope", a) ?? "", /anchor that does not exist/);
  assert.equal(checkFragment("/docs/showcase#pick-one", a), undefined);
  assert.equal(checkFragment("/docs/showcase#marketplace", a), undefined);
  assert.equal(checkFragment("/docs/showcase", a), undefined);
});
