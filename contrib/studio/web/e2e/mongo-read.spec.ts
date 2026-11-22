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

test("documents are read a page at a time, with the exact count", async ({ page, serviceStudio }) => {
  const requests: string[] = [];
  page.on("request", (r) => {
    if (/\/collections\/people\/documents\?/.test(r.url())) {
      requests.push(r.url());
    }
  });
  await openCollection(page, serviceStudio, "people");
  const grid = page.getByRole("grid", { name: "people documents" });
  const nav = page.getByRole("navigation", { name: "Pagination" });
  const position = page.getByTestId("grid-position");

  await expect(grid.getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();
  await expect(position).toHaveText("Rows 1 to 25 of 230");
  // One page of 25 documents and the header row are in the DOM.
  await expect(grid.getByRole("row")).toHaveCount(26);
  expect(requests).toHaveLength(1);
  const first = new URL(requests[0] ?? "");
  expect(first.searchParams.get("perPage")).toBe("25");
  expect(first.searchParams.get("page")).toBe("1");

  await nav.getByRole("button", { name: "Next page" }).click();
  await expect(position).toHaveText("Rows 26 to 50 of 230");
  await expect(nav.getByTestId("grid-page")).toHaveText("Page 2 of 10");

  await nav.getByRole("button", { name: "Last page" }).click();
  await expect(position).toHaveText("Rows 226 to 230 of 230");
  await expect(nav.getByRole("button", { name: "Next page" })).toBeDisabled();

  await nav.getByLabel("Rows per page").selectOption("100");
  await expect(position).toHaveText("Rows 1 to 100 of 230");
  await expect(nav.getByTestId("grid-page")).toHaveText("Page 1 of 3");
  expect(Math.max(...requests.map((u) => Number(new URL(u).searchParams.get("perPage"))))).toBe(100);

  // The server bounds the page size of the documents too.
  const over = await page.request.get(`${serviceStudio.origin}/api/v1/sources/mongo/collections/people/documents?perPage=101`);
  expect(over.status()).toBe(422);
});

test("sorting by a field returns to the first page, and the last page of a sorted view is reachable", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");
  const nav = page.getByRole("navigation", { name: "Pagination" });
  const position = page.getByTestId("grid-position");
  await nav.getByRole("button", { name: "Next page" }).click();
  await expect(position).toHaveText("Rows 26 to 50 of 230");

  await page.getByLabel("Sort by").selectOption("name");
  await expect(position).toHaveText("Rows 1 to 25 of 230");
  await nav.getByRole("button", { name: "Last page" }).click();
  await expect(position).toHaveText("Rows 226 to 230 of 230");
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
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 18 of 18");

  await page.getByRole("button", { name: /Remove filter/ }).click();
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 25 of 230");
});

test("a nested field path filters, and a field path with an operator is refused before it is sent", async ({ page, serviceStudio }) => {
  await openCollection(page, serviceStudio, "people");

  await page.getByLabel("Field").fill("address.city");
  await page.getByLabel("Value").fill("london");
  await page.getByLabel("Condition").selectOption("eq");
  await page.getByRole("button", { name: "Add filter" }).click();
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 1 of 1");

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
  await expect(page.getByRole("note")).toContainText("first 100,000 documents");

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
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 18 of 18");

  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Export JSON Lines" }).click()]);
  expect(download.suggestedFilename()).toBe("people.jsonl");
  const { readFileSync } = await import("node:fs");
  const lines = readFileSync(await download.path(), "utf8").trim().split("\n");
  expect(lines).toHaveLength(18);
  expect(lines.every((l) => (JSON.parse(l) as { age: number }).age > 80)).toBe(true);
});
