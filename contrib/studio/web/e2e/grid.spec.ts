import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

/** Console errors include a blocked inline style or script, so an empty list proves the CSP held. */
function watchErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error" || msg.type() === "warning") {
      errors.push(msg.text());
    }
  });
  page.on("pageerror", (err) => errors.push(err.message));
  return errors;
}

async function open(page: Page, studio: { startUrl: string; origin: string }, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
}

test("a large table renders a window of rows and stays inside the CSP", async ({ page, studio }) => {
  const errors = watchErrors(page);
  await open(page, studio, "events");

  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  // Rows 100,000 plus the header row.
  await expect(grid).toHaveAttribute("aria-rowcount", "100001");
  await expect(grid).toHaveAttribute("aria-colcount", "8");

  const rendered = await grid.getByRole("row").count();
  expect(rendered).toBeLessThanOrEqual(100);
  expect(rendered).toBeGreaterThan(10);

  await expect(page.getByTestId("grid-position")).toHaveText(/Rows 1 to \d+ of about 100,000/);

  // Row placement is set through the CSSOM: no inline style attribute is in the markup the server sent.
  const html = await (await page.request.get(`${studio.origin}/ui/s/db/t/events`)).text();
  expect(html).not.toMatch(/\sstyle=/);

  expect(errors).toEqual([]);
});

test("sorting by a header is done by the server and shown with aria-sort", async ({ page, studio }) => {
  await open(page, studio, "events");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button", { name: "amount", exact: true }).click();
  await expect(page).toHaveURL(/sort=amount$/);
  await expect(grid.getByRole("columnheader", { name: /^amount/ })).toHaveAttribute("aria-sort", "ascending");

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button", { name: "amount", exact: true }).click();
  await expect(page).toHaveURL(/sort=amount&desc=1$/);
  await expect(grid.getByRole("columnheader", { name: /^amount/ })).toHaveAttribute("aria-sort", "descending");
  await expect(grid.getByRole("gridcell", { name: "150000", exact: true })).toBeVisible();

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button", { name: "amount", exact: true }).click();
  await expect(grid.getByRole("columnheader", { name: /^amount/ })).not.toHaveAttribute("aria-sort", /.*/);
});

test("NULL is a token, numbers and booleans are typed", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.locator(".token-null").first()).toHaveText("NULL");
  await expect(grid.locator(".token-bool").first()).toBeVisible();
  await expect(grid.locator(".kind-number").first()).toBeVisible();
  await expect(grid.locator(".token-binary").first()).toHaveText("<binary 2 bytes>");
});

test("the exact count replaces the estimate", async ({ page, studio }) => {
  await open(page, studio, "events");
  await expect(page.getByTestId("grid-position")).toBeVisible();
  const button = page.getByRole("button", { name: "Exact count" });
  if (await button.isVisible()) {
    await button.click();
  }
  await expect(page.getByTestId("grid-position")).toHaveText(/of 100,000$/);
});

test("aria-rowcount is the estimate plus the header row", async ({ page, studio, request }) => {
  await open(page, studio, "events");
  const tables = await page.request.get(`${studio.origin}/api/v1/sources/db/tables`);
  const estimate = ((await tables.json()) as { tables: { name: string; rowEstimate: number }[] }).tables.find(
    (t) => t.name === "events",
  )?.rowEstimate;
  expect(estimate).toBe(100000);
  await expect(page.getByRole("grid", { name: "events rows" })).toHaveAttribute("aria-rowcount", String((estimate ?? 0) + 1));
  void request;
});

const active = "[data-active='true']";

// The Windows clipboard turns line feeds into CRLF.
const lines = (text: string): string[] => text.split(/\r?\n/);

test("the keyboard moves the active cell and the grid has one tab stop", async ({ page, studio }) => {
  await open(page, studio, "events");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  await grid.getByRole("gridcell", { name: "kind-1", exact: true }).first().click();
  await expect(grid.locator(active)).toHaveAttribute("aria-colindex", "3");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "0");

  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowRight");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "2");
  await expect(grid.locator(active)).toHaveAttribute("data-c", "3");
  await expect(grid.locator(active)).toBeFocused();

  // Exactly one cell can be reached with Tab.
  expect(await grid.locator("[tabindex='0']").count()).toBe(1);

  await page.keyboard.press("PageDown");
  await expect(grid.locator(active)).not.toHaveAttribute("data-r", "2");
  await page.keyboard.press("Home");
  await expect(grid.locator(active)).toHaveAttribute("data-c", "0");
  await page.keyboard.press("Control+Home");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "0");

  // Up from the first row reaches the header, where Enter sorts.
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowUp");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "-1");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/sort=kind$/);
  await expect(grid.locator(active)).toBeFocused();
});

test("Ctrl+End reaches the last row of a keyset view", async ({ page, studio }) => {
  await open(page, studio, "events");
  const grid = page.getByRole("grid", { name: "events rows" });
  await grid.getByRole("gridcell", { name: "kind-1", exact: true }).first().click();

  await page.keyboard.press("Control+End");
  await expect(page.getByTestId("grid-position")).toHaveText(/Rows \d[\d,]* to 100,000 of 100,000/);
  await expect(grid.getByRole("gridcell", { name: "100000", exact: true })).toBeVisible();
  await expect(grid.locator(active)).toBeFocused();
  await expect(page.getByTestId("grid-live")).toContainText("Row 100,000 of 100,000");

  await page.keyboard.press("Control+Home");
  await expect(grid.getByRole("gridcell", { name: "1", exact: true }).first()).toBeVisible();
  await expect(grid.locator(active)).toHaveAttribute("data-r", "0");
});

test("Space selects, Ctrl+C copies the cell or the selected rows", async ({ page, studio, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: studio.origin });
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("Control+c");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("bolt");
  await expect(page.getByTestId("grid-live")).toContainText("Copied the cell");

  await page.keyboard.press("Space");
  await page.keyboard.press("Shift+ArrowDown");
  await expect(grid.getByRole("row", { selected: true })).toHaveCount(2);
  await expect(page.getByTestId("grid-live")).toContainText("rows selected");

  await page.keyboard.press("Control+c");
  const text = await page.evaluate(() => navigator.clipboard.readText());
  expect(lines(text)[0]).toBe(["id", "name", "qty", "note", "price", "active", "born", "meta", "raw"].join("\t"));
  expect(lines(text)[1]).toContain("bolt");
  expect(lines(text)[2]).toContain("nut");

  await page.keyboard.press("Escape");
  await expect(grid.getByRole("row", { selected: true })).toHaveCount(0);
});

test("a menu offers copy as JSON and CSV and closes with Escape", async ({ page, studio, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: studio.origin });
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.getByRole("button", { name: "Actions for row 2" }).click();
  await page.getByRole("menuitem", { name: "Copy row as JSON" }).click();
  const json = JSON.parse(await page.evaluate(() => navigator.clipboard.readText())) as Record<string, unknown>[];
  expect(json[0]).toMatchObject({ id: 2, name: "nut", qty: 7, note: null, active: false });

  await grid.getByRole("columnheader", { name: /^name/ }).getByRole("button", { name: /Column menu/ }).click();
  await page.getByRole("menuitem", { name: "Sort descending" }).click();
  await expect(page).toHaveURL(/sort=name&desc=1$/);

  await grid.getByRole("columnheader", { name: /^name/ }).getByRole("button", { name: /Column menu/ }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("menu")).toBeHidden();
});
