import { expect, test } from "./fixtures";

test("read-only: Enter, F2 and a double click open no editor", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/items`);
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
  await expect(page.getByTestId("mode-badge")).toHaveText(/read-only/i);

  const noEditor = page.getByRole("group", { name: /Editor for|Cell editor/ });

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).dblclick();
  await expect(noEditor).toHaveCount(0);

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("F2");
  await expect(noEditor).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // Enter has no edit to start, so it opens the row to read it.
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "items 1" })).toBeVisible();
  await expect(noEditor).toHaveCount(0);
  await page.keyboard.press("Escape");
});

test("read-only: no New row, no Delete, no n shortcut", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/items`);
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await expect(page.getByRole("button", { name: /New row/ })).toHaveCount(0);

  await grid.getByRole("button", { name: "Actions for row 1" }).click();
  await expect(page.getByRole("menuitem", { name: "Open row" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /Delete/ })).toHaveCount(0);
  await page.keyboard.press("Escape");

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("n");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "items 1" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Delete/ })).toHaveCount(0);
});

test("read-only: the write routes of the API answer 404 and change nothing", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  const base = `${studio.origin}/api/v1/sources/db/tables/users`;

  const insert = await page.request.post(`${base}/rows`, { data: { fields: { id: { text: "99" }, name: { text: "x" } } } });
  const update = await page.request.patch(`${base}/rows/1`, { data: { fields: { name: { text: "hacked" } } } });
  const remove = await page.request.delete(`${base}/rows/1`, { data: { confirm: "1" } });

  expect([insert.status(), update.status(), remove.status()]).toEqual([404, 404, 404]);

  const row = (await (await page.request.get(`${base}/rows/1`)).json()) as { cells: { t: string }[] };
  expect(row.cells[1]?.t).toBe("ada");
  const gone = await page.request.get(`${base}/rows/99`);
  expect(gone.status()).toBe(404);
});

test("write mode shows the badge and the write affordances", async ({ page, writeStudio }) => {
  await page.goto(writeStudio.startUrl);
  await page.goto(`${writeStudio.origin}/ui/s/db/t/users`);
  await expect(page.getByTestId("mode-badge")).toHaveText(/write enabled/i);
  await expect(page.getByRole("button", { name: "New row" })).toBeVisible();
});
