import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, studio: { startUrl: string; origin: string }, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
}

async function addFilter(page: Page, column: string, condition: string, value?: string): Promise<void> {
  const form = page.getByRole("form", { name: "Add a filter" });
  await form.getByLabel("Column").selectOption(column);
  await form.getByLabel("Condition").selectOption({ label: condition });
  if (value !== undefined) {
    await form.getByLabel("Value").fill(value);
  }
  await form.getByRole("button", { name: "Add filter" }).click();
}

test("adds a filter, which the server applies and the URL keeps", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await addFilter(page, "name", "starts with", "w");
  await expect(page).toHaveURL(/f=name%3Astarts%3Aw$/);
  await expect(grid.getByRole("gridcell", { name: "washer", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toHaveCount(0);
  await expect(grid).toHaveAttribute("aria-rowcount", "2");
  await expect(page.getByTestId("grid-position")).toHaveText(/of 1$/);
  await expect(page.getByRole("list", { name: /Active filters/ }).getByText('name starts with "w"')).toBeVisible();
});

test("combines filters with AND and removes one", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await addFilter(page, "qty", "is at least", "7");
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toHaveCount(0);
  await expect(grid.getByRole("gridcell", { name: "nut", exact: true })).toBeVisible();

  await addFilter(page, "name", "contains", "ash");
  await expect(grid.getByRole("gridcell", { name: "washer", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "nut", exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(/f=qty%3Agte%3A7&f=name%3Acontains%3Aash$/);

  await page.getByRole("button", { name: 'Remove filter qty is at least "7"' }).click();
  await expect(page).toHaveURL(/f=name%3Acontains%3Aash$/);
  await expect(grid.getByRole("gridcell", { name: "washer", exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Clear filters" }).click();
  await expect(page).not.toHaveURL(/f=/);
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
});

test("a reload keeps the filters and the sort", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await addFilter(page, "qty", "is at least", "7");
  await grid.getByRole("columnheader", { name: /^name/ }).getByRole("button", { name: "name", exact: true }).click();
  await grid.getByRole("columnheader", { name: /^name/ }).getByRole("button", { name: "name", exact: true }).click();
  await expect(page).toHaveURL(/f=qty%3Agte%3A7&sort=name&desc=1$/);

  await page.reload();

  await expect(grid.getByRole("gridcell", { name: "washer", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toHaveCount(0);
  await expect(grid.getByRole("columnheader", { name: /^name/ })).toHaveAttribute("aria-sort", "descending");
  await expect(page.getByRole("list", { name: /Active filters/ }).getByText('qty is at least "7"')).toBeVisible();
  // Descending by name: washer, nut.
  await expect(grid.getByRole("row").nth(1)).toContainText("washer");
});

test("values with colons and percent signs reach the server unchanged", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await addFilter(page, "note", "contains", "100%");
  await expect(page.getByTestId("grid-position")).toHaveText("No rows");

  await page.getByRole("button", { name: /Remove filter/ }).click();
  await addFilter(page, "meta", "contains", '"k":1');
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
  await expect(grid).toHaveAttribute("aria-rowcount", "2");
});

test("NULL conditions need no value and the operators follow the column type", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
  const form = page.getByRole("form", { name: "Add a filter" });

  await form.getByLabel("Column").selectOption("note");
  await expect(form.getByLabel("Condition").getByRole("option", { name: "is greater than" })).toHaveCount(0);
  await form.getByLabel("Column").selectOption("qty");
  await expect(form.getByLabel("Condition").getByRole("option", { name: "is greater than" })).toHaveCount(1);

  await form.getByLabel("Column").selectOption("note");
  await form.getByLabel("Condition").selectOption({ label: "is empty (NULL)" });
  await expect(form.getByLabel("Value")).toHaveCount(0);
  await form.getByRole("button", { name: "Add filter" }).click();
  await expect(page).toHaveURL(/f=note%3Anull%3A$/);
  await expect(grid.getByRole("gridcell", { name: "nut", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toHaveCount(0);
});

test("a filter that does not parse is reported, not hidden", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/items?f=name:regex:a`);
  await expect(page.getByRole("status").filter({ hasText: "not understood" })).toBeVisible();
});

test("the column menu can start a filter on that column", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.getByRole("columnheader", { name: /^qty/ }).getByRole("button", { name: /Column menu/ }).click();
  await page.getByRole("menuitem", { name: "Filter by this column" }).click();
  await expect(page.getByRole("form", { name: "Add a filter" }).getByLabel("Column")).toHaveValue("qty");
  await expect(page.getByRole("form", { name: "Add a filter" }).getByLabel("Value")).toBeFocused();
});
