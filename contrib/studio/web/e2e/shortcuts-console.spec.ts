import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
}

const names = (page: Page): Promise<string[]> =>
  page.getByRole("tab").evaluateAll((els) => els.map((e) => (e.textContent ?? "").replace(/\s*\(unsaved changes\)/, "").trim()));

async function focusBody(page: Page): Promise<void> {
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
}

test("the tab shortcuts work from the page: q then n, ] and [, w then c", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await focusBody(page);

  await page.keyboard.press("q");
  await page.keyboard.press("n");
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");

  await page.keyboard.press("]");
  await expect(page.getByRole("tab", { name: /Query 1/ })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("[");
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");

  await page.keyboard.press("w");
  await page.keyboard.press("c");
  expect(await names(page)).toEqual(["Query 1"]);

  // n alone is not a shortcut here, and q alone does nothing.
  await page.keyboard.press("n");
  await page.keyboard.press("q");
  expect(await names(page)).toEqual(["Query 1"]);
});

test("the same actions are buttons", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await page.getByRole("button", { name: "Previous tab" }).click();
  await expect(page.getByRole("tab", { name: /Query 1/ })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("button", { name: "Next tab" }).click();
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("button", { name: "Close tab" }).click();
  expect(await names(page)).toEqual(["Query 1"]);
});

test("no registry action fires inside the editor", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await editor.click();
  await page.keyboard.type("qn][wc?/rn");
  await expect(editor).toHaveValue("qn][wc?/rn");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(await names(page)).toEqual(["Query 1"]);
});

test("no registry action fires in the find bar, the history search, a dialog field or a select", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await editor.click();
  await page.keyboard.type("select 1");
  await page.keyboard.press("Escape");

  await page.keyboard.press("Control+f");
  const find = page.getByRole("textbox", { name: "Find" });
  await find.fill("");
  await page.keyboard.type("qn][wc?");
  await expect(find).toHaveValue("qn][wc?");
  expect(await names(page)).toEqual(["Query 1"]);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "History and saved queries" }).click();
  const search = page.getByRole("searchbox", { name: "Search the history" });
  await search.click();
  await page.keyboard.type("qn][wc?");
  await expect(search).toHaveValue("qn][wc?");
  expect(await names(page)).toEqual(["Query 1"]);

  await editor.click();
  await page.keyboard.press("Control+s");
  const name = page.getByRole("dialog", { name: "Save query" }).getByLabel("Name");
  await name.fill("");
  await page.keyboard.type("qn][wc?");
  await expect(name).toHaveValue("qn][wc?");
  await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toHaveCount(0);
  await page.keyboard.press("Escape");
  expect(await names(page)).toEqual(["Query 1"]);

  const picker = page.getByLabel("Open a table in a tab");
  await picker.focus();
  await page.keyboard.press("q");
  await page.keyboard.press("n");
  await page.keyboard.press("]");
  expect(await names(page)).toEqual(["Query 1"]);
});

test("the documented editor keys work, and Ctrl+S and Ctrl+F are the editor's only while it has the focus", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await editor.click();
  await page.keyboard.type("select 1 as one");
  await page.keyboard.press("Escape");

  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();
  await page.keyboard.press("Control+/");
  await expect(editor).toHaveValue("-- select 1 as one");
  await page.keyboard.press("Control+/");
  await page.keyboard.type(" wh");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Space");
  await expect(page.getByRole("listbox", { name: "Completions" })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+f");
  await expect(page.getByRole("textbox", { name: "Find" })).toBeFocused();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+s");
  await expect(page.getByRole("dialog", { name: "Save query" })).toBeVisible();
  await page.keyboard.press("Escape");

  // Outside the editor the browser keeps both.
  await focusBody(page);
  await page.keyboard.press("Control+s");
  await page.keyboard.press("Control+f");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Find" })).toHaveCount(0);
});

test("the help dialog lists the console shortcuts and the editor keys", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("button", { name: "New query" }).click();
  await focusBody(page);
  await page.keyboard.press("?");
  const dialog = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(dialog).toBeVisible();

  const anywhere = dialog.getByRole("table", { name: "Shortcuts" });
  for (const text of ["q then n", "w then c", "]", "["]) {
    await expect(anywhere.locator("kbd", { hasText: text })).toBeVisible();
  }
  const editorKeys = dialog.getByRole("table", { name: "Editor keys" });
  for (const text of ["Ctrl+Enter", "Ctrl+Shift+Enter", "Ctrl+/", "Ctrl+S", "Ctrl+F", "Ctrl+Space"]) {
    await expect(editorKeys.locator("kbd", { hasText: text }).first()).toBeVisible();
  }
  await expect(dialog.getByRole("table", { name: "Tab keys" })).toBeVisible();
  // Never the browser's own tab and window keys.
  await expect(dialog.getByText(/Ctrl\+T|Ctrl\+W|Ctrl\+N/)).toHaveCount(0);
});
