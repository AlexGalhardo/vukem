import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

interface Studio {
  startUrl: string;
  origin: string;
}

async function open(page: Page, studio: Studio, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
}

const position = (page: Page) => page.getByTestId("grid-position");
const pageLabel = (page: Page) => page.getByTestId("grid-page");
const control = (page: Page, name: string) => page.getByRole("navigation", { name: "Pagination" }).getByRole("button", { name, exact: true });
const sizeSelect = (page: Page) => page.getByRole("navigation", { name: "Pagination" }).getByLabel("Rows per page");
const active = "[data-active='true']";

/** The row requests of the grid, so a spec can say what the browser asked the server for. */
function watchRows(page: Page): string[] {
  const urls: string[] = [];
  page.on("request", (r) => {
    if (/\/tables\/[^/]+\/rows\?/.test(r.url())) {
      urls.push(r.url());
    }
  });
  return urls;
}

test("the footer says which rows and how many, 25 a page by default, and one page is all that is fetched", async ({ page, studio }) => {
  const requests = watchRows(page);
  await open(page, studio, "tickets");
  const grid = page.getByRole("grid", { name: "tickets rows" });
  await expect(grid.getByRole("gridcell", { name: "ticket 1", exact: true })).toBeVisible();

  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");
  await expect(pageLabel(page)).toHaveText("Page 1 of 2");
  await expect(sizeSelect(page)).toHaveValue("25");
  // Every row of the page is there, in the page: 25 rows and the header row.
  await expect(grid.getByRole("row")).toHaveCount(26);
  await expect(grid.getByRole("gridcell", { name: "ticket 25", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "ticket 26", exact: true })).toHaveCount(0);
  await expect(grid).toHaveAttribute("aria-rowcount", "28");

  await expect(control(page, "First page")).toBeDisabled();
  await expect(control(page, "Previous page")).toBeDisabled();
  await expect(control(page, "Next page")).toBeEnabled();
  await expect(control(page, "Last page")).toBeEnabled();

  // Only the current page was asked for, with a bounded size.
  expect(requests).toHaveLength(1);
  const url = new URL(requests[0] ?? "");
  expect(url.searchParams.get("perPage")).toBe("25");
  expect(url.searchParams.get("page")).toBe("1");
});

test("next, previous, last and first move between pages, and each asks the server for that page only", async ({ page, studio }) => {
  const requests = watchRows(page);
  await open(page, studio, "tickets");
  const grid = page.getByRole("grid", { name: "tickets rows" });
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");

  await control(page, "Next page").click();
  await expect(position(page)).toHaveText("Rows 26 to 27 of 27");
  await expect(pageLabel(page)).toHaveText("Page 2 of 2");
  await expect(grid.getByRole("gridcell", { name: "ticket 26", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "ticket 1", exact: true })).toHaveCount(0);
  await expect(grid.getByRole("row")).toHaveCount(3);
  await expect(control(page, "Next page")).toBeDisabled();
  await expect(control(page, "Last page")).toBeDisabled();
  await expect(control(page, "Previous page")).toBeEnabled();
  // The row numbers and the ARIA row indexes continue from the page before.
  await expect(grid.getByRole("row").nth(1)).toHaveAttribute("aria-rowindex", "27");
  await expect(grid.getByRole("checkbox", { name: "Select row 26" })).toBeVisible();

  await control(page, "Previous page").click();
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");

  await control(page, "Last page").click();
  await expect(position(page)).toHaveText("Rows 26 to 27 of 27");
  await control(page, "First page").click();
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");

  const pages = requests.map((u) => new URL(u).searchParams.get("page"));
  expect(pages).toEqual(["1", "2", "1", "2", "1"]);
});

test("the page size is 25, 50, 75 or 100, is remembered, and changing it returns to page 1", async ({ page, studio }) => {
  await open(page, studio, "tickets");
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");
  await expect(sizeSelect(page).locator("option")).toHaveText(["25", "50", "75", "100"]);

  await control(page, "Next page").click();
  await expect(pageLabel(page)).toHaveText("Page 2 of 2");

  await sizeSelect(page).selectOption("50");
  await expect(position(page)).toHaveText("Rows 1 to 27 of 27");
  await expect(pageLabel(page)).toHaveText("Page 1 of 1");
  await expect(control(page, "Next page")).toBeDisabled();
  await expect(control(page, "Last page")).toBeDisabled();

  // The choice is kept for this browser.
  await page.reload();
  await expect(sizeSelect(page)).toHaveValue("50");
  await expect(position(page)).toHaveText("Rows 1 to 27 of 27");
  expect(await page.evaluate(() => window.localStorage.getItem("studio.pageSize"))).toBe("50");

  // A stored value the interface does not offer is ignored.
  await page.evaluate(() => window.localStorage.setItem("studio.pageSize", "13"));
  await page.reload();
  await expect(sizeSelect(page)).toHaveValue("25");
});

test("sorting and filtering return to the first page", async ({ page, studio }) => {
  await open(page, studio, "tickets");
  const grid = page.getByRole("grid", { name: "tickets rows" });
  await control(page, "Next page").click();
  await expect(pageLabel(page)).toHaveText("Page 2 of 2");

  await grid.getByRole("columnheader", { name: /^title/ }).getByRole("button", { name: "title", exact: true }).click();
  await expect(page).toHaveURL(/sort=title$/);
  await expect(pageLabel(page)).toHaveText("Page 1 of 2");
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");

  await control(page, "Last page").click();
  await expect(pageLabel(page)).toHaveText("Page 2 of 2");
  const form = page.getByRole("form", { name: "Add a filter" });
  await form.getByLabel("Column").selectOption("id");
  await form.getByLabel("Condition").selectOption({ label: "is at least" });
  await form.getByLabel("Value").fill("20");
  await form.getByRole("button", { name: "Add filter" }).click();
  await expect(position(page)).toHaveText("Rows 1 to 8 of 8");
  await expect(pageLabel(page)).toHaveText("Page 1 of 1");
});

test("the server refuses a page larger than 100 and the browser never asks for more", async ({ page, studio }) => {
  await open(page, studio, "events");
  await expect(position(page)).toHaveText(/^Rows 1 to 25 of (about )?100,000$/);
  const base = `${studio.origin}/api/v1/sources/db/tables/events/rows`;

  for (const bad of ["101", "200", "1000", "0", "-1", "abc"]) {
    expect((await page.request.get(`${base}?perPage=${bad}`)).status(), `perPage=${bad}`).toBe(422);
  }
  const ok = await page.request.get(`${base}?perPage=100&page=3`);
  expect(ok.status()).toBe(200);
  expect(((await ok.json()) as { rows: unknown[] }).rows).toHaveLength(100);

  const requests = watchRows(page);
  await sizeSelect(page).selectOption("100");
  await expect(position(page)).toHaveText(/^Rows 1 to 100 of 100,000$/);
  for (const size of ["75", "50", "25"]) {
    await sizeSelect(page).selectOption(size);
    await expect(position(page)).toHaveText(new RegExp(`^Rows 1 to ${size} of 100,000$`));
  }
  const sizes = requests.map((u) => Number(new URL(u).searchParams.get("perPage")));
  expect(sizes.length).toBeGreaterThan(0);
  expect(Math.max(...sizes)).toBeLessThanOrEqual(100);
});

test("Last page reaches the end of a 100,000-row table, in the default order and sorted", async ({ page, studio }) => {
  await open(page, studio, "events");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(position(page)).toHaveText("Rows 1 to 25 of 100,000");
  await expect(pageLabel(page)).toHaveText("Page 1 of 4,000");

  await control(page, "Last page").click();
  await expect(position(page)).toHaveText("Rows 99,976 to 100,000 of 100,000");
  await expect(pageLabel(page)).toHaveText("Page 4,000 of 4,000");
  await expect(grid.getByRole("gridcell", { name: "100000", exact: true })).toBeVisible();

  await open(page, studio, "events?sort=amount");
  await control(page, "Last page").click();
  await expect(position(page)).toHaveText("Rows 99,976 to 100,000 of 100,000");
  await expect(grid.getByRole("gridcell", { name: "150000", exact: true })).toBeVisible();
});

test("Last row and Ctrl+End go to the last row of the whole table, Ctrl+Home to the first", async ({ page, studio }) => {
  await open(page, studio, "tickets");
  const grid = page.getByRole("grid", { name: "tickets rows" });
  await grid.getByRole("gridcell", { name: "ticket 3", exact: true }).click();

  await page.keyboard.press("Control+End");
  await expect(position(page)).toHaveText("Rows 26 to 27 of 27");
  await expect(grid.getByRole("gridcell", { name: "ticket 27", exact: true })).toBeVisible();
  await expect(grid.locator(active)).toBeFocused();
  await expect(grid.locator(active)).toHaveAttribute("data-r", "1");
  await expect(page.getByTestId("grid-live")).toContainText("Row 27 of 27");

  // Already on the last page: it only moves the focus.
  await grid.getByRole("gridcell", { name: "ticket 26", exact: true }).click();
  await page.keyboard.press("Control+End");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "1");

  await page.keyboard.press("Control+Home");
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "0");

  // The button does what the key does.
  await page.getByRole("button", { name: "Last row" }).click();
  await expect(position(page)).toHaveText("Rows 26 to 27 of 27");
  await expect(grid.locator("[data-r='1'][data-c='1']")).toHaveAttribute("data-active", "true");
});

test("Open row, Copy cell, selection and the keyboard work on the second page", async ({ page, studio, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: studio.origin });
  await open(page, studio, "tickets");
  const grid = page.getByRole("grid", { name: "tickets rows" });
  await control(page, "Next page").click();
  await expect(grid.getByRole("gridcell", { name: "ticket 26", exact: true })).toBeVisible();

  // The keyboard moves inside the page and stops at its last row.
  await grid.getByRole("gridcell", { name: "ticket 26", exact: true }).click();
  await page.keyboard.press("ArrowDown");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "1");
  await page.keyboard.press("ArrowDown");
  await expect(grid.locator(active)).toHaveAttribute("data-r", "1");

  await page.getByRole("button", { name: "Copy cell" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("ticket 27");

  // Open row opens the row by its key, which is not its position in the page.
  await page.getByRole("button", { name: "Open row" }).click();
  await expect(page.getByRole("dialog", { name: "tickets 27" })).toBeVisible();
  await page.getByRole("button", { name: "Close the row" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await grid.getByRole("button", { name: "Actions for row 26" }).click();
  await page.getByRole("menuitem", { name: "Copy row as JSON" }).click();
  const json = JSON.parse(await page.evaluate(() => navigator.clipboard.readText())) as { id: number }[];
  expect(json[0]?.id).toBe(26);

  // A selection belongs to its page: leaving the page clears it.
  await grid.getByRole("checkbox", { name: "Select row 26" }).check();
  await expect(grid.getByRole("row", { selected: true })).toHaveCount(1);
  await control(page, "Previous page").click();
  await expect(position(page)).toHaveText("Rows 1 to 25 of 27");
  await expect(grid.getByRole("row", { selected: true })).toHaveCount(0);
});

test("an export is the whole filtered result, not the page", async ({ page, studio }) => {
  await open(page, studio, "tickets");
  await control(page, "Next page").click();
  await expect(position(page)).toHaveText("Rows 26 to 27 of 27");

  const href = await page.getByRole("link", { name: "Export JSON Lines" }).getAttribute("href");
  expect(href).not.toMatch(/page|perPage/);
  const body = await (await page.request.get(`${studio.origin}${href}`)).text();
  expect(body.trim().split("\n")).toHaveLength(27);
});

test("an inline edit on the second page is saved, and deleting its last row leaves no empty page", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "pager_rw");
  const grid = page.getByRole("grid", { name: "pager_rw rows" });
  await expect(position(page)).toHaveText("Rows 1 to 25 of 26");
  await control(page, "Next page").click();
  await expect(position(page)).toHaveText("Rows 26 to 26 of 26");

  await grid.getByRole("gridcell", { name: "row 26", exact: true }).dblclick();
  const field = page.getByRole("group", { name: "Editor for title" }).getByRole("textbox", { name: "Edit title" });
  await field.fill("edited 26");
  await field.press("Enter");
  await expect(grid.getByRole("gridcell", { name: "edited 26", exact: true })).toBeVisible();
  await expect(page.getByTestId("grid-live")).toContainText("Saved title of row 26");
  await expect(pageLabel(page)).toHaveText("Page 2 of 2");

  await grid.getByRole("button", { name: "Actions for row 26" }).click();
  await page.getByRole("menuitem", { name: "Delete row…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete this row?" });
  await dialog.getByLabel("Type the key to confirm").fill("26");
  await dialog.getByRole("button", { name: "Delete row" }).click();

  // The last page no longer exists: the grid is on the last page that does, with its rows.
  await expect(position(page)).toHaveText("Rows 1 to 25 of 25");
  await expect(pageLabel(page)).toHaveText("Page 1 of 1");
  await expect(grid.getByRole("gridcell", { name: "row 25", exact: true })).toBeVisible();
  await expect(page.getByText("No rows")).toHaveCount(0);
});
