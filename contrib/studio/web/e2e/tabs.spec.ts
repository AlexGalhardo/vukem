import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
}

const tabNames = (page: Page): Promise<string[]> =>
  page.getByRole("tab").evaluateAll((els) => els.map((e) => (e.textContent ?? "").replace(/\s*\((unsaved changes|pinned)\)/g, "").trim()));

test("the strip is an ARIA tablist with one tab stop and a panel for the selected tab", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "New query" }).click();

  await expect(page.getByRole("tablist", { name: "Open tabs" })).toBeVisible();
  const tabs = page.getByRole("tab");
  await expect(tabs).toHaveCount(3);
  await expect(page.getByRole("tab", { name: /Query 3/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tab", { name: /Query 3/ })).toHaveAttribute("tabindex", "0");
  await expect(page.getByRole("tab", { name: /Query 1/ })).toHaveAttribute("tabindex", "-1");

  const panel = page.getByRole("tabpanel");
  await expect(panel).toHaveCount(1);
  await expect(panel).toHaveAccessibleName(/Query 3/);
  const controls = await page.getByRole("tab", { name: /Query 3/ }).getAttribute("aria-controls");
  expect(await panel.getAttribute("id")).toBe(controls);
});

test("keyboard only: open, switch, reorder with Alt+Arrow, close with Delete", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);

  await page.getByRole("button", { name: "New query" }).focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  expect(await tabNames(page)).toEqual(["Query 1", "Query 2", "Query 3"]);

  await page.getByRole("tab", { name: /Query 3/ }).focus();
  await page.keyboard.press("ArrowLeft");
  await expect(page.getByRole("tab", { name: /Query 2/ })).toBeFocused();
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeFocused();
  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: /Query 3/ })).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeFocused();

  await page.keyboard.press("Alt+ArrowRight");
  expect(await tabNames(page)).toEqual(["Query 2", "Query 1", "Query 3"]);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeFocused();
  await page.keyboard.press("Alt+ArrowRight");
  await page.keyboard.press("Alt+ArrowRight");
  expect(await tabNames(page)).toEqual(["Query 2", "Query 3", "Query 1"]);

  await page.keyboard.press("Delete");
  expect(await tabNames(page)).toEqual(["Query 2", "Query 3"]);
  await expect(page.getByRole("tab", { name: /Query 3/ })).toBeFocused();
  await expect(page.getByRole("tab", { name: /Query 3/ })).toHaveAttribute("aria-selected", "true");
});

test("an edited query shows the unsaved marker and asks before it closes", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 42");
  await page.keyboard.press("Escape");

  const tab = page.getByRole("tab", { name: /Query 1/ });
  await expect(tab).toContainText("(unsaved changes)");
  await tab.focus();
  await page.keyboard.press("Delete");

  const dialog = page.getByRole("dialog", { name: "Close Query 1?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(tab).toBeFocused();
  await expect(page.getByRole("tab")).toHaveCount(1);

  await page.keyboard.press("Delete");
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "Close tab" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("tab")).toHaveCount(0);
  await expect(page.getByText("No tab is open")).toBeVisible();
});

test("an empty query closes without asking", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("tab", { name: /Query 2/ }).focus();
  await page.keyboard.press("Delete");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("tab")).toHaveCount(1);
});

test("the mouse closes a tab with its button and with a middle click", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "Close Query 2" }).click();
  expect(await tabNames(page)).toEqual(["Query 1", "Query 3"]);
  await page.getByRole("tab", { name: /Query 3/ }).click({ button: "middle" });
  expect(await tabNames(page)).toEqual(["Query 1"]);
});

test("a tab can be dragged to a new place", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("tab", { name: /Query 3/ }).dragTo(page.getByRole("tab", { name: /Query 1/ }));
  expect(await tabNames(page)).toEqual(["Query 3", "Query 1", "Query 2"]);
});

test("pin keeps a tab first, duplicate copies the text, close others keeps pinned", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 1 as one");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "New query" }).click();

  await page.getByRole("tab", { name: /Query 3/ }).click();
  await page.getByRole("button", { name: "Pin tab" }).click();
  expect(await tabNames(page)).toEqual(["Query 3", "Query 1", "Query 2"]);
  await expect(page.getByRole("tab", { name: /Query 3/ })).toContainText("(pinned)");

  await page.getByRole("tab", { name: /Query 1/ }).click();
  await page.getByRole("button", { name: "Duplicate tab" }).click();
  expect(await tabNames(page)).toEqual(["Query 3", "Query 1", "Query 4", "Query 2"]);
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toHaveValue("select 1 as one");

  await page.getByRole("button", { name: "Close other tabs" }).click();
  // The copy and the original hold unsaved text: Studio asks first.
  await page.getByRole("dialog", { name: "Close the other tabs?" }).getByRole("button", { name: "Close others" }).click();
  expect(await tabNames(page)).toEqual(["Query 3", "Query 4"]);
});

test("at most 20 tabs open, and the limit is announced", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const add = page.getByRole("button", { name: "New query" });
  for (let i = 0; i < 24; i++) {
    await add.click();
  }
  await expect(page.getByRole("tab")).toHaveCount(20);
  await expect(page.getByText("20 tabs are open. Close one to open another.")).toBeVisible();
});

test("a table opens in a tab beside the queries and is opened only once", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const picker = page.getByLabel("Open a table in a tab");
  await picker.selectOption("users");
  await expect(page.getByRole("tab", { name: /users/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("grid", { name: "users rows" })).toBeVisible();

  await page.getByRole("tab", { name: /Query 1/ }).click();
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeVisible();
  await picker.selectOption("users");
  await expect(page.getByRole("tab")).toHaveCount(2);
  await expect(page.getByRole("tab", { name: /users/ })).toHaveAttribute("aria-selected", "true");
});

test("tabs and query text survive a reload, results do not", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 'secret-result-value' as v");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("gridcell", { name: "secret-result-value" })).toBeVisible();

  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 2");
  await page.keyboard.press("Escape");
  await page.getByLabel("Open a table in a tab").selectOption("posts");
  await page.getByRole("tab", { name: /Query 1/ }).click();

  await page.reload();
  expect(await tabNames(page)).toEqual(["Query 1", "Query 2", "posts"]);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toHaveValue("select 'secret-result-value' as v");
  // The result is gone: it was never stored.
  await expect(page.getByRole("grid", { name: "Query result" })).toHaveCount(0);

  const stored = await page.evaluate(() => JSON.stringify({ ...window.sessionStorage }));
  expect(stored).toContain("select 2");
  expect(stored).not.toContain('"result"');
  const local = await page.evaluate(() => JSON.stringify({ ...window.localStorage }));
  expect(local).not.toContain("select 2");
});

test("the write mode switch of a tab resets when it reopens", async ({ page, writeStudio }) => {
  await open(page, writeStudio.origin, writeStudio.startUrl);
  await page.getByRole("switch", { name: "Write mode" }).click();
  await expect(page.getByTestId("console-mode")).toHaveText("Write mode");
  await page.getByRole("button", { name: "New query" }).click();
  await expect(page.getByTestId("console-mode")).toHaveText("Read-only");
  await page.getByRole("tab", { name: /Query 1/ }).click();
  await expect(page.getByTestId("console-mode")).toHaveText("Write mode");
  await page.reload();
  await expect(page.getByTestId("console-mode")).toHaveText("Read-only");
});
