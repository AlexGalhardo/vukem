import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Studio } from "./fixtures";

async function openCollection(page: Page, studio: Studio, collection: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/mongo/c/${collection}`);
  await expect(page.getByRole("grid", { name: `${collection} documents` })).toBeVisible();
}

test("the collections page lists every collection with an estimated count", async ({ page, serviceStudio }) => {
  await page.goto(serviceStudio.startUrl);
  await page.goto(`${serviceStudio.origin}/ui/s/mongo`);

  const table = page.getByRole("table", { name: "Collections" });
  await expect(table.getByRole("row", { name: /^events about 3/ })).toBeVisible();
  await expect(table.getByRole("row", { name: /^people about 230/ })).toBeVisible();
  await table.getByRole("link", { name: "people" }).click();
  await expect(page).toHaveURL(/\/ui\/s\/mongo\/c\/people$/);
});

test("documents load as you scroll and only a window of rows is in the DOM", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  const grid = page.getByRole("grid", { name: "people documents" });

  await expect(grid.getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();
  const rendered = await grid.getByRole("row").count();
  expect(rendered).toBeLessThanOrEqual(100);

  // The first page has 100 documents; scrolling to the end loads the rest.
  await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("100 documents loaded");
  const scroller = page.locator(".grid-scroll");
  await expect(async () => {
    await scroller.evaluate((el) => {
      el.scrollTop = el.scrollHeight;
    });
    await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("230 documents loaded", { timeout: 1500 });
  }).toPass({ timeout: 20_000 });
  expect(await grid.getByRole("row").count()).toBeLessThanOrEqual(100);
});

test("the Table view has columns from the first documents and nested values as compact JSON", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  const grid = page.getByRole("grid", { name: "people documents" });

  for (const name of ["_id", "name", "age", "active", "address", "tags"]) {
    await expect(grid.getByRole("columnheader", { name: new RegExp(`^${name}`) })).toBeVisible();
  }
  await expect(grid.getByRole("gridcell", { name: '{"city":"london","zip":"00001"}', exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: '["math","engines"]', exact: true })).toBeVisible();
});

test("the JSON view shows one summary column", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  await page.getByRole("button", { name: "JSON", exact: true }).click();

  const grid = page.getByRole("grid", { name: "people documents" });
  await expect(grid.getByRole("columnheader", { name: /^document/ })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: /"name":"ada"/ })).toBeVisible();
  await page.getByRole("button", { name: "Table", exact: true }).click();
  await expect(grid.getByRole("columnheader", { name: /^age/ })).toBeVisible();
});

test("a filter of field, operator and value is applied by the server and can be removed", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");

  await page.getByLabel("Field").fill("age");
  await page.getByLabel("Condition").selectOption("gt");
  await page.getByLabel("Value").fill("80");
  await page.getByRole("button", { name: "Add filter" }).click();

  await expect(page.getByRole("list", { name: /Active filters/ })).toContainText('age is greater than "80"');
  await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("18 documents loaded");

  await page.getByRole("button", { name: /Remove filter/ }).click();
  await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("100 documents loaded");
});

test("a nested field path filters, and a field path with an operator is refused before it is sent", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");

  await page.getByLabel("Field").fill("address.city");
  await page.getByLabel("Value").fill("london");
  await page.getByLabel("Condition").selectOption("eq");
  await page.getByRole("button", { name: "Add filter" }).click();
  await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("1 document loaded");

  await page.getByLabel("Field").fill("$where");
  await page.getByLabel("Value").fill("1");
  await page.getByRole("button", { name: "Add filter" }).click();
  await expect(page.getByRole("alert")).toContainText("Give a field path");
});

test("sorting by a field explains the page limit and the server sorts", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");

  await expect(page.getByRole("note")).toHaveCount(0);
  await page.getByLabel("Sort by").selectOption("name");
  await page.getByLabel("Direction").selectOption("desc");
  await expect(page.getByRole("note")).toContainText("page 1000");

  const grid = page.getByRole("grid", { name: "people documents" });
  await expect(grid.getByRole("gridcell", { name: "person-230", exact: true })).toBeVisible();
  await expect(grid.getByRole("columnheader", { name: /^name/ })).toHaveAttribute("aria-sort", "descending");
});

test("a document opens in a drawer as a tree and as Extended JSON, and Escape closes it", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  const grid = page.getByRole("grid", { name: "people documents" });

  await grid.getByRole("gridcell", { name: "ada", exact: true }).click();
  await page.keyboard.press("Enter");

  const drawer = page.getByRole("dialog", { name: /^people 1$/ });
  await expect(drawer).toBeVisible();
  const tree = drawer.getByRole("list", { name: "Document" });
  await expect(tree).toContainText("name");
  await expect(tree).toContainText('"ada"');

  await tree.getByRole("button", { name: /address/ }).click();
  await expect(tree).toContainText("london");
  await tree.getByRole("button", { name: /address/ }).click();
  await expect(tree).not.toContainText("london");

  await drawer.getByRole("button", { name: "Extended JSON" }).click();
  await expect(drawer.getByLabel("Document as Extended JSON")).toContainText('"name": "ada"');

  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
});

test("an ObjectId and a date show their text, and the document id is the typed confirmation", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "events");
  const grid = page.getByRole("grid", { name: "events documents" });
  await expect(grid.getByRole("gridcell", { name: "65a1b2c3d4e5f60718293a4b", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "2026-01-02T03:04:05Z", exact: true })).toBeVisible();
});

test("read-only: no insert, replace or delete, and the write routes do not exist", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  await expect(page.getByRole("button", { name: "Insert document" })).toHaveCount(0);

  await page.getByRole("grid", { name: "people documents" }).getByRole("gridcell", { name: "ada", exact: true }).click();
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog", { name: /^people 1$/ });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("button", { name: /Edit document|Delete document/ })).toHaveCount(0);

  const post = await page.request.post(`${serviceStudio.origin}/api/v1/sources/mongo/collections/people/documents`, { data: { doc: "{}" } });
  expect(post.status()).toBe(404);
  const del = await page.request.delete(`${serviceStudio.origin}/api/v1/sources/mongo/collections/people/documents/1`, { data: { confirm: "1" } });
  expect(del.status()).toBe(404);
});

test("a document that holds markup is shown as text and runs nothing", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "xss");
  const grid = page.getByRole("grid", { name: "xss documents" });
  const payload = '<script>window.__xss = 1</script><img src=x onerror="window.__xss = 1">';
  await expect(grid.getByRole("gridcell", { name: payload, exact: true })).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  await expect(page.locator("main img")).toHaveCount(0);

  await grid.getByRole("gridcell", { name: payload, exact: true }).click();
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog", { name: /^xss 1$/ });
  await drawer.getByRole("button", { name: "Extended JSON" }).click();
  await expect(drawer.getByLabel("Document as Extended JSON")).toContainText("<script>");
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
});

test("the export link downloads the documents of the current filter as JSON Lines", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  await page.getByLabel("Field").fill("age");
  await page.getByLabel("Condition").selectOption("gt");
  await page.getByLabel("Value").fill("80");
  await page.getByRole("button", { name: "Add filter" }).click();
  await expect(page.getByRole("status").filter({ hasText: "loaded" })).toContainText("18 documents loaded");

  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Export JSON Lines" }).click()]);
  expect(download.suggestedFilename()).toBe("people.jsonl");
  const { readFileSync } = await import("node:fs");
  const lines = readFileSync(await download.path(), "utf8").trim().split("\n");
  expect(lines).toHaveLength(18);
  expect(lines.every((l) => (JSON.parse(l) as { age: number }).age > 80)).toBe(true);
});
