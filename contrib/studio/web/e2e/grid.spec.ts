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

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button").click();
  await expect(page).toHaveURL(/sort=amount$/);
  await expect(grid.getByRole("columnheader", { name: /^amount/ })).toHaveAttribute("aria-sort", "ascending");

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button").click();
  await expect(page).toHaveURL(/sort=amount&desc=1$/);
  await expect(grid.getByRole("columnheader", { name: /^amount/ })).toHaveAttribute("aria-sort", "descending");
  await expect(grid.getByRole("gridcell", { name: "150000", exact: true })).toBeVisible();

  await grid.getByRole("columnheader", { name: /^amount/ }).getByRole("button").click();
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
