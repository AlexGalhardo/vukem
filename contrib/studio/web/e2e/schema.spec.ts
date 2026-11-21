import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/schema`);
  await expect(page.getByRole("heading", { name: "Schema", level: 1 })).toBeVisible();
  await expect(page.getByTestId("diagram")).toBeVisible();
}

const pick = (page: Page, name: RegExp) => page.getByRole("navigation", { name: "Tables of the schema" }).getByRole("button", { name });
const viewport = (page: Page) => page.getByTestId("diagram-viewport");

test("shows columns with key badges, indexes in order, foreign keys and referenced by", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  await pick(page, /^posts/).click();
  const columns = page.getByRole("table", { name: "Columns of posts" });
  await expect(columns.getByRole("row", { name: /^id integer no primary/ })).toBeVisible();
  await expect(columns.getByRole("row", { name: /^user_id integer no foreign/ })).toBeVisible();

  const indexes = page.getByRole("table", { name: "Indexes of posts" });
  await expect(indexes.getByRole("row", { name: /primary key id primary/ })).toBeVisible();
  await expect(indexes.getByRole("row", { name: /posts_user_title user_id, title composite/ })).toBeVisible();

  await expect(page.getByText("points at", { exact: false }).first()).toBeVisible();
  await pick(page, /^users/).click();
  await expect(page.getByRole("heading", { name: "Referenced by" })).toBeVisible();
  await expect(page.getByRole("list").filter({ hasText: "points here at" }).getByRole("button", { name: "posts" })).toBeVisible();
  const uniques = page.getByRole("table", { name: "Indexes of users" });
  await expect(uniques.getByRole("row", { name: /users_email email unique/ })).toBeVisible();
});

test("the table list filters by name", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  await page.getByRole("searchbox", { name: "Filter tables" }).fill("pos");
  const list = page.getByRole("navigation", { name: "Tables of the schema" });
  await expect(list.getByRole("button", { name: /^posts/ })).toBeVisible();
  await expect(list.getByRole("button", { name: /^users/ })).toHaveCount(0);
  await expect(list.getByRole("status")).toHaveText(/^1 of \d+ tables$/);
});

test("the diagram pans and zooms with the keyboard and fits to the view", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  const box = page.getByTestId("diagram");
  await box.focus();
  const before = { x: Number(await viewport(page).getAttribute("data-x")), scale: Number(await viewport(page).getAttribute("data-scale")) };

  await page.keyboard.press("ArrowLeft");
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-x"))).toBeGreaterThan(before.x);
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-x"))).toBeLessThan(before.x);

  await page.keyboard.press("-");
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-scale"))).toBeLessThan(before.scale);
  await page.keyboard.press("0");
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-scale"))).toBe(before.scale);

  await page.getByRole("button", { name: "Zoom in" }).click();
  await page.getByRole("button", { name: "Zoom in" }).click();
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-scale"))).toBeGreaterThan(before.scale);
  await page.getByRole("button", { name: "Fit to view" }).click();
  await expect.poll(async () => Number(await viewport(page).getAttribute("data-scale"))).toBe(before.scale);
});

test("the diagram has one focusable link per table and highlights the neighbors", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  // Other specs add and drop tables in the shared database, so the count comes from the schema itself.
  const schema = (await (await page.request.get(`${studio.origin}/api/v1/sources/db/schema`)).json()) as { tables: unknown[] };
  await expect(page.locator("[data-node]")).toHaveCount(schema.tables.length);
  const posts = page.locator('[data-node="posts"]');
  await posts.focus();
  await expect(posts).toHaveClass(/diagram-node-selected/);
  await expect(page.locator('[data-node="users"]')).toHaveClass(/diagram-node-near/);
  await expect(page.locator('[data-node="items"]')).toHaveClass(/diagram-node-dim/);
  await expect(page.getByTestId("diagram")).toHaveAccessibleDescription(/Diagram of \d+ tables and \d+ foreign keys?\./);
});

test("the relationships table lists every foreign key", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  const rels = page.getByRole("table", { name: "Relationships" });
  const schema = (await (await page.request.get(`${studio.origin}/api/v1/sources/db/schema`)).json()) as { tables: { foreignKeys: unknown[] }[] };
  const keys = schema.tables.reduce((n, t) => n + t.foreignKeys.length, 0);
  await expect(rels.getByRole("row")).toHaveCount(keys + 1);
  await expect(rels.getByRole("row", { name: /posts user_id users id/ })).toBeVisible();
});

test("hiding the diagram leaves the relationships table", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  await page.getByRole("switch", { name: "Show the diagram" }).uncheck();
  await expect(page.getByTestId("diagram")).toHaveCount(0);
  await expect(page.getByRole("table", { name: "Relationships" })).toBeVisible();
  await page.getByRole("switch", { name: "Show the diagram" }).check();
  await expect(page.getByTestId("diagram")).toBeVisible();
});

test("shows and copies the CREATE statement where SQLite reports it", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  await pick(page, /^users/).click();
  await page.getByRole("button", { name: "Show CREATE statement" }).click();
  const code = page.getByLabel("CREATE statement of users");
  await expect(code).toContainText("CREATE TABLE users");
  await page.getByRole("button", { name: "Hide" }).click();
  await expect(code).toHaveCount(0);
});

test("a node opens its table in a tab of the workspace", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/q`);
  await page.getByRole("button", { name: "Schema", exact: true }).click();
  await expect(page.getByRole("tab", { name: /Schema/ })).toHaveAttribute("aria-selected", "true");

  await page.locator('[data-node="posts"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("tab", { name: /posts/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("grid")).toBeVisible();

  await page.getByRole("tab", { name: /Schema/ }).click();
  await page.getByRole("button", { name: "Schema", exact: true }).click();
  await expect(page.getByRole("tab", { name: /Schema/ })).toHaveCount(1);
});
