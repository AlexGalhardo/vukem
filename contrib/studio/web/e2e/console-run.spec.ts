import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function openConsole(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
}

const endless = "with recursive c(x) as (select 1 union all select x + 1 from c) select count(*) from c";

test("a select of 3,000 rows shows only 1000 and says so", async ({ page, studio }) => {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select id, kind from events limit 3000");
  await page.getByRole("button", { name: "Run" }).click();

  await expect(page.getByRole("status").filter({ hasText: /1000 rows in/ })).toBeVisible();
  await expect(page.getByText("limited to 1000 rows")).toBeVisible();
  const grid = page.getByRole("grid", { name: "Query result" });
  await expect(grid).toHaveAttribute("aria-rowcount", "1001");
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();
  // The grid is virtualized: far fewer rows than 1000 are in the page.
  expect(await grid.getByRole("row").count()).toBeLessThan(150);
  expect(errors).toEqual([]);
});

test("the result sorts and exports from the client", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select id, name from users order by id");
  await page.keyboard.press("Control+Enter");
  const grid = page.getByRole("grid", { name: "Query result" });
  await expect(grid.getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();

  const [csv] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Download CSV" }).click()]);
  expect(csv.suggestedFilename()).toBe("result.csv");
  const [json] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Download JSON" }).click()]);
  expect(json.suggestedFilename()).toBe("result.json");

  await grid.getByRole("columnheader", { name: /^name/ }).click();
  await grid.getByRole("columnheader", { name: /^name/ }).click();
  await expect(grid.getByRole("columnheader", { name: /^name/ })).toHaveAttribute("aria-sort", "descending");
});

test("a run can be cancelled", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type(endless);
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("status").filter({ hasText: /Running/ })).toBeVisible();

  await page.getByRole("button", { name: "Cancel" }).first().click();
  await expect(page.getByRole("status").filter({ hasText: "Cancelled" })).toBeVisible();

  // The server is free again: the next statement answers at once.
  await page.getByRole("textbox", { name: "SQL for db" }).press("Control+a");
  await page.keyboard.type("select 7 as seven");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("gridcell", { name: "7", exact: true })).toBeVisible();
});

test("an invalid statement shows a generic error with its kind", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select * from table_that_is_not_there");
  await page.keyboard.press("Control+Enter");

  const alert = page.getByRole("alert");
  await expect(alert).toContainText("Database error");
  await expect(alert).not.toContainText("table_that_is_not_there");
  await expect(alert).not.toContainText("no such table");

  await page.keyboard.press("Control+a");
  await page.keyboard.type("select 1; select 2");
  await page.keyboard.press("Control+Shift+Enter");
  await expect(alert).toContainText("Input error");
});

test("explain shows the plan as text", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select * from users where id = 1");
  await page.getByRole("button", { name: "Explain" }).click();
  await expect(page.getByLabel("Query plan")).toContainText(/SEARCH|SCAN/);
});

test("focus returns to the editor after a run from the button", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1");
  await page.getByRole("button", { name: "Run" }).click();
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();
});

test("an empty editor asks for a statement", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "Run" }).click();
  await expect(page.getByText("Type a statement to run.")).toBeVisible();
});

test("a statement that changes nothing says how long it took", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("pragma table_info(users)");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("status").filter({ hasText: /\d+ rows? in/ })).toBeVisible();
});
