import type { Locator, Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function openConsole(page: Page, origin: string, startUrl: string): Promise<Locator> {
  const errors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") {
      errors.push(m.text());
    }
  });
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await expect(editor).toBeVisible();
  await editor.click();
  (page as Page & { cspErrors?: string[] }).cspErrors = errors;
  return editor;
}

function cspErrors(page: Page): string[] {
  return (page as Page & { cspErrors?: string[] }).cspErrors ?? [];
}

test("typing draws the highlight layer, the line numbers and the caret position", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 'a''b', 12 -- note\nfrom users");

  await expect(page.locator(".tok-keyword", { hasText: /^select$/ })).toBeVisible();
  await expect(page.locator(".tok-string", { hasText: "'a''b'" })).toBeVisible();
  await expect(page.locator(".tok-number", { hasText: "12" })).toBeVisible();
  await expect(page.locator(".tok-comment", { hasText: "-- note" })).toBeVisible();
  await expect(page.locator(".sqled-num")).toHaveText(["1", "2"]);
  await expect(page.getByText("Line 2, column 11")).toBeVisible();
  await expect(editor).toHaveValue("select 'a''b', 12 -- note\nfrom users");

  expect(cspErrors(page)).toEqual([]);
});

test("the highlight layer lines up with the text area", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1\nfrom users");
  const first = await page.locator(".sqled-line").nth(0).boundingBox();
  const second = await page.locator(".sqled-line").nth(1).boundingBox();
  const area = await editor.boundingBox();
  expect(first && area && Math.abs(first.y - (area.y + 8)) < 1).toBe(true);
  expect(first && second && Math.abs(second.y - first.y - 20) < 1).toBe(true);
});

test("Tab indents, and Escape then Tab leaves the editor", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1");
  await page.keyboard.press("Home");
  await page.keyboard.press("Tab");
  await expect(editor).toHaveValue("  select 1");
  await expect(editor).toBeFocused();

  await page.keyboard.press("Shift+Tab");
  await expect(editor).toHaveValue("select 1");

  await page.keyboard.press("Escape");
  await page.keyboard.press("Tab");
  await expect(editor).not.toBeFocused();
  await expect(editor).toHaveValue("select 1");
});

test("Enter keeps the indentation of the line", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select\n");
  await page.keyboard.press("Tab");
  await page.keyboard.type("a,");
  await page.keyboard.press("Enter");
  await page.keyboard.type("b");
  await expect(editor).toHaveValue("select\n  a,\n  b");
});

test("Ctrl+Enter runs the statement at the cursor, Ctrl+Shift+Enter all the text", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1;\nselect 2;\nselect 3");
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByTestId("run-request")).toHaveText("select 2");

  await page.keyboard.press("Control+Shift+Enter");
  await expect(page.getByTestId("run-request")).toHaveText("select 1;\nselect 2;\nselect 3");

  await page.keyboard.press("Control+a");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByTestId("run-request")).toHaveText("select 1;\nselect 2;\nselect 3");
});

test("Ctrl+/ toggles a comment on the line", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select 1");
  await page.keyboard.press("Control+/");
  await expect(editor).toHaveValue("-- select 1");
  await page.keyboard.press("Control+/");
  await expect(editor).toHaveValue("select 1");
});

test("Ctrl+F opens the find bar only in the editor, steps through matches and Escape returns", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select a from users\nunion select b from users");
  await page.keyboard.press("Control+f");

  const find = page.getByRole("textbox", { name: "Find" });
  await expect(find).toBeFocused();
  await find.fill("users");
  await expect(page.getByRole("status").filter({ hasText: "1 of 2" })).toBeVisible();
  await expect(page.locator(".sqled-box-current")).toHaveCount(1);

  await page.keyboard.press("Enter");
  await expect(page.getByRole("status").filter({ hasText: "2 of 2" })).toBeVisible();
  await page.keyboard.press("Shift+Enter");
  await expect(page.getByRole("status").filter({ hasText: "1 of 2" })).toBeVisible();

  await find.fill("zzz");
  await expect(page.getByText("No matches")).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(find).toHaveCount(0);
  await expect(editor).toBeFocused();
});

test("completion lists tables and columns, and Enter accepts", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select * from us");
  const list = page.getByRole("listbox", { name: "Completions" });
  await expect(list).toBeVisible();
  await expect(list.getByRole("option").first()).toHaveText(/users/);
  await expect(editor).toHaveAttribute("aria-activedescendant", /.+/);

  await page.keyboard.press("Enter");
  await expect(editor).toHaveValue("select * from users");
  await expect(list).toHaveCount(0);

  await page.keyboard.type(" u where u.");
  await page.keyboard.press("Control+Space");
  await expect(list.getByRole("option", { name: /email/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(list).toHaveCount(0);
  await expect(editor).toBeFocused();
});

test("brackets next to the caret are marked", async ({ page, studio }) => {
  await openConsole(page, studio.origin, studio.startUrl);
  await page.keyboard.type("select (1 + 2)");
  await expect(page.locator(".sqled-box-bracket")).toHaveCount(2);
});

test("the editor labels itself and describes the Tab behavior", async ({ page, studio }) => {
  const editor = await openConsole(page, studio.origin, studio.startUrl);
  await expect(editor).toHaveAccessibleDescription(/Escape, then Tab/);
  await expect(page.locator(".sqled-gutter")).toHaveAttribute("aria-hidden", "true");
  await expect(page.locator(".sqled-clip")).toHaveAttribute("aria-hidden", "true");
});
