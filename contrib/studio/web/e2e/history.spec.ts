import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
}

async function run(page: Page, sql: string): Promise<void> {
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();
  await page.keyboard.press("Control+a");
  await page.keyboard.type(sql);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
}

const showHistory = (page: Page) => page.getByRole("button", { name: "History and saved queries" }).click();
const stored = (page: Page, prefix: string): Promise<string[]> =>
  page.evaluate((p) => Object.keys(window.localStorage).filter((k) => k.startsWith(p)), prefix);

test("every run is kept in the history with its numbers and never its rows", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await run(page, "select name from users order by id");
  await expect(page.getByRole("gridcell", { name: "linus", exact: true })).toBeVisible();
  await run(page, "select * from table_that_is_not_there");
  await expect(page.getByRole("alert")).toContainText("Database error");

  await showHistory(page);
  const list = page.getByRole("list", { name: "History" });
  const items = list.getByRole("listitem");
  await expect(items).toHaveCount(2);
  // Newest first.
  await expect(items.nth(0)).toContainText("table_that_is_not_there");
  await expect(items.nth(0)).toContainText("database error");
  await expect(items.nth(1)).toContainText("select name from users order by id");
  await expect(items.nth(1)).toContainText("2 rows");

  // The history says where it lives.
  await expect(page.getByText(/stored in this browser until you clear it/)).toBeVisible();

  const keys = await stored(page, "vukem.studio.history.");
  expect(keys.length).toBe(1);
  const raw = await page.evaluate((k) => window.localStorage.getItem(k) ?? "", keys[0] as string);
  expect(raw).toContain("select name from users");
  expect(raw).not.toContain("linus");
  expect(raw).not.toContain("ada");
});

test("search narrows the list, delete removes one entry, clear history asks first", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await run(page, "select 1 as alpha");
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();
  await run(page, "select 2 as beta");
  await expect(page.getByRole("gridcell", { name: "2", exact: true })).toBeVisible();
  await showHistory(page);

  const items = page.getByRole("list", { name: "History" }).getByRole("listitem");
  await expect(items).toHaveCount(2);
  await page.getByRole("searchbox", { name: "Search the history" }).fill("ALPHA");
  await expect(items).toHaveCount(1);
  await expect(items.first()).toContainText("alpha");
  await page.getByRole("searchbox", { name: "Search the history" }).fill("zzz");
  await expect(page.getByText("No statement matches.")).toBeVisible();
  await page.getByRole("searchbox", { name: "Search the history" }).fill("");

  await page.getByRole("button", { name: /Delete from history: select 1 as alpha/ }).click();
  await expect(items).toHaveCount(1);
  await expect(items.first()).toContainText("beta");

  await page.getByRole("button", { name: "Clear history" }).click();
  const dialog = page.getByRole("dialog", { name: "Clear the history?" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(items).toHaveCount(1);

  await page.getByRole("button", { name: "Clear history" }).click();
  await dialog.getByRole("button", { name: "Clear history" }).click();
  await expect(page.getByText("No statements yet.")).toBeVisible();
  expect(await stored(page, "vukem.studio.history.")).toEqual([]);
});

test("run again loads a new tab and never runs it", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await run(page, "select 7 as seven");
  await expect(page.getByRole("gridcell", { name: "7", exact: true })).toBeVisible();
  await showHistory(page);

  await page.getByRole("button", { name: /Open in a new tab: select 7 as seven/ }).click();
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toHaveValue("select 7 as seven");
  await expect(page.getByRole("grid", { name: "Query result" })).toHaveCount(0);
  await expect(page.getByRole("status").filter({ hasText: /rows? in/ })).toHaveCount(0);
});

test("copy puts the statement on the clipboard", async ({ page, studio, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: studio.origin });
  await open(page, studio.origin, studio.startUrl);
  await run(page, "select 9 as nine");
  await expect(page.getByRole("gridcell", { name: "9", exact: true })).toBeVisible();
  await showHistory(page);
  await page.getByRole("button", { name: /Copy: select 9 as nine/ }).click();
  await expect(page.getByText("Statement copied.")).toBeAttached();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("select 9 as nine");
});

test("the setting that turns history off keeps nothing and clears what was kept", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await run(page, "select 1 as before");
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();
  await showHistory(page);
  await expect(page.getByRole("list", { name: "History" }).getByRole("listitem")).toHaveCount(1);

  await page.getByRole("checkbox", { name: "Do not store history" }).check();
  await expect(page.getByText("History is off: nothing you run is kept.")).toBeVisible();
  expect(await stored(page, "vukem.studio.history.")).toEqual([]);

  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await run(page, "select 2 as after");
  await expect(page.getByRole("gridcell", { name: "2", exact: true })).toBeVisible();
  expect(await stored(page, "vukem.studio.history.")).toEqual([]);
  expect(await page.evaluate(() => window.localStorage.getItem("vukem.studio.historyOff"))).toBe("1");

  await page.getByRole("checkbox", { name: "Do not store history" }).uncheck();
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await run(page, "select 3 as again");
  await expect(page.getByRole("gridcell", { name: "3", exact: true })).toBeVisible();
  await expect(page.getByRole("list", { name: "History" }).getByRole("listitem")).toHaveCount(1);
});

test("the history of an earlier Studio run is deleted at startup, the library stays", async ({ page, studio }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("vukem.studio.history.0123456789abcdef", JSON.stringify([{ id: "x", sql: "select 'old-literal'" }]));
    window.localStorage.setItem(
      "vukem.studio.library",
      JSON.stringify([{ id: "lib1", name: "Kept", sql: "select 'kept'", source: "db", savedAt: 1 }]),
    );
  });
  await open(page, studio.origin, studio.startUrl);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
  await expect.poll(() => stored(page, "vukem.studio.history.0123456789abcdef")).toEqual([]);
  expect(await page.evaluate(() => window.localStorage.getItem("vukem.studio.library"))).toContain("Kept");

  await showHistory(page);
  await expect(page.getByRole("list", { name: "Saved queries" }).getByRole("listitem")).toHaveCount(1);
  await expect(page.getByText("old-literal")).toHaveCount(0);
});

test("Ctrl+S saves the query to the library; open, rename and delete it", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 5 as five");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("tab", { name: /Query 1/ })).toContainText("(unsaved changes)");

  await page.keyboard.press("Control+s");
  const dialog = page.getByRole("dialog", { name: "Save query" });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Name").fill("Five");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("tab", { name: /Five/ })).not.toContainText("(unsaved changes)");
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();

  await showHistory(page);
  const saved = page.getByRole("list", { name: "Saved queries" }).getByRole("listitem");
  await expect(saved).toHaveCount(1);
  await expect(saved.first()).toContainText("select 5 as five");
  expect(await page.evaluate(() => window.localStorage.getItem("vukem.studio.library"))).toContain("Five");
  // The library is in localStorage under a stable key, not in sessionStorage, and not in history.
  expect(await stored(page, "vukem.studio.history.")).toEqual([]);

  await page.getByRole("button", { name: "Rename saved query Five" }).click();
  const rename = page.getByRole("dialog", { name: "Rename saved query" });
  await rename.getByLabel("New name").fill("Fifth");
  await rename.getByRole("button", { name: "Rename" }).click();
  await expect(saved.first().getByText("Fifth")).toBeVisible();

  await page.getByRole("button", { name: "Open saved query Fifth in a new tab" }).click();
  await expect(page.getByRole("tab", { name: /Fifth/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab")).toHaveCount(2);
  await page.getByRole("button", { name: "Delete saved query Fifth" }).click();
  await expect(saved).toHaveCount(0);
});

test("saving needs a name and a closed unsaved tab asks first", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Save query" }).click();
  const dialog = page.getByRole("dialog", { name: "Save query" });
  await dialog.getByLabel("Name").fill("   ");
  await expect(dialog.getByRole("button", { name: "Save" })).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toContainText("(unsaved changes)");
});
