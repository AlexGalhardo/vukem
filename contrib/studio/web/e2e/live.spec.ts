import { expect, test } from "./fixtures";

test("Live is off by default; turning it on refetches the current page and keeps the page and the focus", async ({
  page,
  liveStudio,
}) => {
  await page.goto(liveStudio.startUrl);
  await page.goto(`${liveStudio.origin}/ui/s/db/t/events`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  const live = page.getByRole("switch", { name: "Live" });
  await expect(live).not.toBeChecked();

  // Go to the second page (rows 26 to 50: row 46 is its 21st) and put the focus on a cell.
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 26 to 50 of 100,000");
  await expect(grid.locator("[data-r='20'][data-c='2']")).toBeVisible();
  await grid.locator("[data-r='20'][data-c='2']").click();
  await expect(grid.locator("[data-active='true']")).toBeFocused();

  // Off: a change in the database does not show up on its own.
  const patch = (note: string) =>
    page.request.patch(`${liveStudio.origin}/api/v1/sources/db/tables/events/rows/46`, {
      data: { fields: { kind: { text: note, null: false } } },
    });
  expect((await patch("changed-1")).status()).toBe(200);
  await page.waitForTimeout(1500);
  await expect(grid.locator("[data-r='20'][data-c='2']")).toHaveText("kind-6");

  await live.check();
  await expect(grid.locator("[data-r='20'][data-c='2']")).toHaveText("changed-1");

  // Still the second page, and the focused cell keeps the focus across refreshes.
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 26 to 50 of 100,000");
  await expect(page.getByTestId("grid-page")).toHaveText("Page 2 of 4,000");
  await grid.locator("[data-r='20'][data-c='2']").click();
  await expect(grid.locator("[data-active='true']")).toBeFocused();

  expect((await patch("changed-2")).status()).toBe(200);
  await expect(grid.locator("[data-r='20'][data-c='2']")).toHaveText("changed-2");
  await expect(page.getByTestId("grid-page")).toHaveText("Page 2 of 4,000");
  await expect(grid.locator("[data-active='true']")).toBeFocused();
  await expect(grid.locator("[data-active='true']")).toHaveAttribute("data-r", "20");

  await live.uncheck();
  expect((await patch("changed-3")).status()).toBe(200);
  await page.waitForTimeout(1500);
  await expect(grid.locator("[data-r='20'][data-c='2']")).toHaveText("changed-2");
});

test("Live sends refresh requests only while it is on", async ({ page, liveStudio }) => {
  await page.goto(liveStudio.startUrl);
  await page.goto(`${liveStudio.origin}/ui/s/db/t/users`);
  const grid = page.getByRole("grid", { name: "users rows" });
  await expect(grid.getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();

  let requests = 0;
  page.on("request", (r) => {
    if (/\/tables\/users\/rows\?/.test(r.url())) {
      requests++;
    }
  });
  await page.waitForTimeout(1500);
  expect(requests).toBe(0);

  await page.getByRole("switch", { name: "Live" }).check();
  await expect.poll(() => requests, { timeout: 5000 }).toBeGreaterThanOrEqual(2);
});

test("the export link downloads the rows of the current filter and sort", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events?f=kind%3Aeq%3Akind-3&sort=amount&desc=1`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-3", exact: true }).first()).toBeVisible();

  // The footer's exact count is the number of rows the export must hold: all of them, not the page.
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 25 of 10,000");

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("link", { name: "Export CSV" }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("events.csv");
  const path = await download.path();
  const { readFileSync } = await import("node:fs");
  const lines = readFileSync(path, "utf8").trim().split(/\r?\n/);
  expect(lines[0]).toBe("id,kind,amount,active,at,payload,note");
  expect(lines.length - 1).toBe(10_000);
  // Sorted by amount descending: the largest kind-3 id comes first.
  expect(lines[1]?.startsWith("99993,kind-3,149989.5")).toBe(true);
  expect(lines.every((l, i) => i === 0 || l.includes(",kind-3,"))).toBe(true);
});

test("the JSON Lines export has one object per filtered row", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/items?f=qty%3Agte%3A7`);
  await expect(page.getByRole("grid", { name: "items rows" }).getByRole("gridcell", { name: "nut", exact: true })).toBeVisible();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("link", { name: "Export JSON Lines" }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("items.jsonl");
  const { readFileSync } = await import("node:fs");
  const rows = readFileSync(await download.path(), "utf8")
    .trim()
    .split("\n")
    .map((l) => JSON.parse(l) as { name: string });
  expect(rows.map((r) => r.name).sort()).toEqual(["nut", "washer"]);
});
