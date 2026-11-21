import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
}

const named = ["button", "textbox", "searchbox", "combobox", "tab", "switch", "checkbox", "link", "listbox", "grid", "tablist", "tabpanel"] as const;

async function everyControlHasAName(page: Page): Promise<void> {
  for (const role of named) {
    for (const el of await page.getByRole(role).all()) {
      if (await el.isVisible()) {
        await expect(el, `a ${role} without an accessible name`).toHaveAccessibleName(/\S/);
      }
    }
  }
}

test("every control of the console has an accessible name, in each state", async ({ page, writeStudio }) => {
  await open(page, writeStudio.origin, writeStudio.startUrl);
  await everyControlHasAName(page);

  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select id, name from users");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("grid", { name: "Query result" })).toBeVisible();
  await everyControlHasAName(page);

  await page.getByRole("button", { name: "New query" }).click();
  await page.getByLabel("Open a table in a tab").selectOption("users");
  await expect(page.getByRole("grid", { name: "users rows" })).toBeVisible();
  await everyControlHasAName(page);

  await page.getByRole("button", { name: "History and saved queries" }).click();
  await everyControlHasAName(page);

  await page.getByRole("tab", { name: /Query 1/ }).click();
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.press("Control+f");
  await everyControlHasAName(page);
  await page.keyboard.press("Escape");
  await page.keyboard.type(" wh");
  await expect(page.getByRole("listbox", { name: "Completions" })).toBeVisible();
  await everyControlHasAName(page);
});

test("regions and widgets are labelled and the highlight layer is hidden from assistive technology", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await expect(page.getByRole("region", { name: "SQL console" })).toBeVisible();
  await expect(page.getByRole("tablist", { name: "Open tabs" })).toBeVisible();
  await expect(page.getByRole("tabpanel", { name: /Query 1/ })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeVisible();

  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 1");
  await expect(page.locator(".sqled-clip")).toHaveAttribute("aria-hidden", "true");
  await expect(page.locator(".sqled-gutter")).toHaveAttribute("aria-hidden", "true");
  // The text is read once, from the text area: nothing inside the hidden layers is exposed.
  expect(await page.locator('.sqled-clip [role], .sqled-clip button, .sqled-clip a, .sqled-clip input').count()).toBe(0);
});

test("a run is announced politely and an error is an alert", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select id from events limit 42");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");

  const status = page.getByRole("status").filter({ hasText: /42 rows in/ });
  await expect(status).toBeVisible();
  // role=status is a polite live region; nothing here is assertive.
  expect(await page.locator('[aria-live="assertive"]').count()).toBe(0);

  await page.keyboard.press("Control+a");
  await page.keyboard.type("select * from nowhere");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("alert")).toContainText("Database error");
});

test("focus returns to where it came from after a run and after each dialog", async ({ page, writeStudio }) => {
  await open(page, writeStudio.origin, writeStudio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await editor.click();
  await page.keyboard.type("update items set qty = qty where id = 4");
  await page.keyboard.press("Escape");

  // After a run started from a button.
  await page.getByRole("button", { name: "Explain" }).click();
  await expect(page.getByLabel("Query plan")).toBeVisible();
  await expect(editor).toBeFocused();

  // After the write confirmation, cancelled and confirmed.
  await page.getByRole("switch", { name: "Write mode" }).click();
  await editor.click();
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("dialog", { name: "Run UPDATE on db?" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(editor).toBeFocused();
  await page.keyboard.press("Control+Enter");
  await page.getByRole("dialog", { name: "Run UPDATE on db?" }).getByRole("button", { name: "Run UPDATE" }).click();
  await expect(page.getByRole("status").filter({ hasText: /affected/ })).toBeVisible();
  await expect(editor).toBeFocused();

  // After the save dialog.
  await page.keyboard.press("Control+s");
  await page.getByRole("dialog", { name: "Save query" }).getByLabel("Name").fill("Same qty");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(editor).toBeFocused();

  // After the close confirmation: the tab. The text is saved now, so edit it first.
  await page.keyboard.type(" ");
  const tab = page.getByRole("tab", { name: /Same qty/ });
  await tab.focus();
  await page.keyboard.press("Delete");
  await expect(page.getByRole("dialog", { name: /Close Same qty/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(tab).toBeFocused();

  // After clearing the history, focus is back on the button that opened the dialog.
  await page.getByRole("button", { name: "History and saved queries" }).click();
  const clear = page.getByRole("button", { name: "Clear history" });
  await clear.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "Clear the history?" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(clear).toBeFocused();
});

test("the console works by keyboard alone, with no trap", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });

  // From the top of the page the Tab key reaches the editor.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  let reached = false;
  for (let i = 0; i < 40 && !reached; i++) {
    await page.keyboard.press("Tab");
    reached = await editor.evaluate((el) => el === document.activeElement);
  }
  expect(reached).toBe(true);

  await page.keyboard.type("select 1 as one");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();

  // Escape then Tab leaves the editor; Tab again reaches the next control, not the editor.
  await page.keyboard.press("Escape");
  await page.keyboard.press("Tab");
  await expect(editor).not.toBeFocused();
  const names: string[] = [];
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    names.push(await page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? document.activeElement?.textContent ?? ""));
  }
  expect(names.some((n) => n.includes("Copy as CSV") || n.includes("Download CSV"))).toBe(true);

  // Shift+Tab goes back to the editor.
  for (let i = 0; i < 30 && !(await editor.evaluate((el) => el === document.activeElement)); i++) {
    await page.keyboard.press("Shift+Tab");
  }
  await expect(editor).toBeFocused();
});

test("in forced colors the text area shows its own text and the highlight layer steps aside", async ({ page, studio }) => {
  await page.emulateMedia({ forcedColors: "active" });
  await open(page, studio.origin, studio.startUrl);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
  await page.keyboard.type("select 1");
  const color = await page.getByRole("textbox", { name: "SQL for db" }).evaluate((el) => getComputedStyle(el).color);
  expect(color).not.toBe("rgba(0, 0, 0, 0)");
  expect(await page.locator(".sqled-layer").evaluate((el) => getComputedStyle(el).display)).toBe("none");
});
