import { expect, test } from "./fixtures";

test("a table without a primary key shows a notice and offers no edit in write mode", async ({ page, writeStudio }) => {
  await page.goto(writeStudio.startUrl);
  await page.goto(`${writeStudio.origin}/ui/s/db/t/audit_lines`);
  const grid = page.getByRole("grid", { name: "audit_lines rows" });
  await expect(grid.getByRole("gridcell", { name: "two", exact: true })).toBeVisible();

  await expect(page.getByRole("status").filter({ hasText: "no primary key" })).toBeVisible();
  await expect(page.getByRole("button", { name: "New row" })).toHaveCount(0);

  await grid.getByRole("gridcell", { name: "two", exact: true }).dblclick();
  await expect(page.getByRole("group", { name: /Editor for/ })).toHaveCount(0);
  await expect(page.getByTestId("grid-live")).toContainText("no primary key");

  await grid.getByRole("gridcell", { name: "two", exact: true }).click();
  await page.keyboard.press("F2");
  await page.keyboard.press("Enter");
  await page.keyboard.press("n");
  await expect(page.getByRole("group", { name: /Editor for/ })).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await grid.getByRole("button", { name: "Actions for row 2" }).click();
  await expect(page.getByRole("menuitem", { name: "Open row" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /Delete/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
});

test("the rows of a table without a primary key can still be read, sorted and filtered", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/audit_lines`);
  const grid = page.getByRole("grid", { name: "audit_lines rows" });
  await expect(grid.getByRole("gridcell", { name: "three", exact: true })).toBeVisible();
  await expect(grid).toHaveAttribute("aria-rowcount", "4");
  await expect(grid.locator(".token-null")).toHaveCount(1);

  await grid.getByRole("columnheader", { name: /^line/ }).getByRole("button", { name: "line", exact: true }).click();
  await expect(grid.getByRole("columnheader", { name: /^line/ })).toHaveAttribute("aria-sort", "ascending");
  await expect(grid.getByRole("row").nth(1)).toContainText("one");
});

test("the API refuses writes to a table without a primary key", async ({ page, writeStudio }) => {
  await page.goto(writeStudio.startUrl);
  const base = `${writeStudio.origin}/api/v1/sources/db/tables/audit_lines/rows`;
  const insert = await page.request.post(base, { data: { fields: { line: { text: "x" } } } });
  const update = await page.request.patch(`${base}/one`, { data: { fields: { line: { text: "x" } } } });
  expect([insert.status(), update.status()]).toEqual([404, 404]);
});

test("a name that is not a table, such as a view, says so", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/audit_view`);
  await expect(page.getByRole("alert")).toContainText("browses tables only");
});
