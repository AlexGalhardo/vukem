import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function openConsole(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
}

async function enableWrite(page: Page): Promise<void> {
  await page.getByRole("switch", { name: "Write mode" }).click();
  await page.getByRole("textbox", { name: "SQL for db" }).click();
}

async function replaceText(page: Page, sql: string): Promise<void> {
  // After a run or a dialog, focus comes back to the editor: wait for it before typing.
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();
  await page.keyboard.press("Control+a");
  await page.keyboard.type(sql);
  // A word typed last may leave the completion list open; Escape closes it without leaving the editor.
  await page.keyboard.press("Escape");
}

async function runAll(page: Page, sql: string): Promise<void> {
  await replaceText(page, sql);
  await page.keyboard.press("Control+Enter");
}

test("Write mode is off when the page opens, and a write is refused until it is on", async ({ page, studio, writeStudio }) => {
  void studio;
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  const mode = page.getByTestId("console-mode");
  const toggle = page.getByRole("switch", { name: "Write mode" });
  await expect(mode).toHaveText("Read-only");
  await expect(toggle).toHaveAttribute("aria-checked", "false");

  await runAll(page, "update items set qty = 99 where id = 4");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText(/Turn on Write mode/)).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Input error");

  await toggle.click();
  await expect(mode).toHaveText("Write mode");
  await expect(toggle).toHaveAttribute("aria-checked", "true");

  await page.reload();
  await expect(page.getByTestId("console-mode")).toHaveText("Read-only");
});

test("an UPDATE with a WHERE needs the confirmation button named for it", async ({ page, writeStudio }) => {
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  await enableWrite(page);
  await runAll(page, "update items set note = 'confirmed' where id = 4");

  const dialog = page.getByRole("dialog", { name: "Run UPDATE on db?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Statement to run")).toHaveText("update items set note = 'confirmed' where id = 4");
  await expect(dialog.getByLabel(/Type db to confirm/)).toHaveCount(0);

  // Escape cancels: nothing ran and focus is back in the editor.
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();
  await expect(page.getByRole("status").filter({ hasText: /affected/ })).toHaveCount(0);

  await page.keyboard.press("Control+Enter");
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Run UPDATE" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("status").filter({ hasText: /1 row affected in/ })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "SQL for db" })).toBeFocused();

  await runAll(page, "select note from items where id = 4");
  await expect(page.getByRole("gridcell", { name: "confirmed", exact: true })).toBeVisible();
});

test("a read runs without a dialog even in write mode", async ({ page, writeStudio }) => {
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  await enableWrite(page);
  await runAll(page, "select 1 as one");
  await expect(page.getByRole("gridcell", { name: "1", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("a DELETE without WHERE needs the source name typed", async ({ page, writeStudio }) => {
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  await enableWrite(page);

  await runAll(page, "create table scratch_delete (id integer primary key, v text)");
  await page.getByRole("dialog", { name: "Run CREATE on db?" }).getByRole("button", { name: "Run CREATE" }).click();
  await expect(page.getByRole("status").filter({ hasText: /Done in|affected/ })).toBeVisible();

  await runAll(page, "insert into scratch_delete values (1, 'a'), (2, 'b'), (3, 'c')");
  await page.getByRole("dialog", { name: "Run INSERT on db?" }).getByRole("button", { name: "Run INSERT" }).click();
  await expect(page.getByRole("status").filter({ hasText: /3 rows affected/ })).toBeVisible();

  await runAll(page, "delete from scratch_delete");
  const dialog = page.getByRole("dialog", { name: "Run DELETE on db?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("alert")).toContainText("no WHERE");

  const confirm = dialog.getByRole("button", { name: "Run DELETE" });
  const name = dialog.getByLabel("Type db to confirm");
  await expect(name).toBeFocused();
  await expect(confirm).toBeDisabled();
  await name.fill("wrong");
  await expect(confirm).toBeDisabled();
  await name.fill("db");
  await expect(confirm).toBeEnabled();

  // Cancel first: the rows are still there.
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await runAll(page, "select count(*) as n from scratch_delete");
  await expect(page.getByRole("gridcell", { name: "3", exact: true })).toBeVisible();

  await runAll(page, "delete from scratch_delete");
  await dialog.getByLabel("Type db to confirm").fill("db");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("status").filter({ hasText: /3 rows affected/ })).toBeVisible();

  await runAll(page, "select count(*) as n from scratch_delete");
  await expect(page.getByRole("gridcell", { name: "0", exact: true })).toBeVisible();
});

test("a DROP asks for the source name too", async ({ page, writeStudio }) => {
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  await enableWrite(page);
  await runAll(page, "create table scratch_drop (id integer)");
  await page.getByRole("dialog", { name: "Run CREATE on db?" }).getByRole("button", { name: "Run CREATE" }).click();
  await expect(page.getByRole("status").filter({ hasText: /Done in|affected/ })).toBeVisible();

  await runAll(page, "drop table scratch_drop");
  const dialog = page.getByRole("dialog", { name: "Run DROP on db?" });
  await expect(dialog.getByLabel("Type db to confirm")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Run DROP" })).toBeDisabled();
  await dialog.getByRole("button", { name: "Cancel" }).click();
});

test("the confirmation dialog keeps focus inside it", async ({ page, writeStudio }) => {
  await openConsole(page, writeStudio.origin, writeStudio.startUrl);
  await enableWrite(page);
  await runAll(page, "update items set qty = 1 where id = 4");
  const dialog = page.getByRole("dialog", { name: "Run UPDATE on db?" });
  await expect(dialog).toBeVisible();

  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    // Focus may rest on the browser's own controls, but never on the page behind the dialog.
    expect(await page.evaluate(() => document.activeElement?.closest(".console-toolbar, .sqled") != null)).toBe(false);
  }
  await page.keyboard.press("Escape");
});
