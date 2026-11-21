import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

interface Studio {
  startUrl: string;
  origin: string;
}

async function open(page: Page, studio: Studio, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
}

const editor = (page: Page, column: string) => page.getByRole("group", { name: `Editor for ${column}` });

test("a double click edits a cell, Enter saves, and the value persists after a reload", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "edit-me", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "edit-me", exact: true }).dblclick();
  await expect(editor(page, "name")).toBeVisible();
  const field = editor(page, "name").getByRole("textbox", { name: "Edit name" });
  await expect(field).toHaveValue("edit-me");
  await expect(field).toBeFocused();

  await field.fill("edited");
  await field.press("Enter");

  await expect(grid.getByRole("gridcell", { name: "edited", exact: true })).toBeVisible();
  await expect(editor(page, "name")).toBeHidden();
  await expect(page.getByTestId("grid-live")).toContainText("Saved name of row 4");
  await expect(grid.getByRole("gridcell", { name: "edited", exact: true })).toBeFocused();

  await page.reload();
  await expect(grid.getByRole("gridcell", { name: "edited", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "edit-me", exact: true })).toHaveCount(0);
});

test("F2 edits by keyboard, Escape cancels and Tab saves and moves right", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "washer", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "washer", exact: true }).click();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("F2");
  await expect(editor(page, "qty").getByRole("textbox", { name: "Edit qty" })).toBeFocused();
  await page.keyboard.type("1");
  await page.keyboard.press("Escape");
  await expect(editor(page, "qty")).toBeHidden();
  await expect(grid.locator("[data-active='true']")).toBeFocused();
  await expect(grid.locator("[data-r='2'][data-c='3']")).toHaveText("9");

  await page.keyboard.press("F2");
  const field = editor(page, "qty").getByRole("textbox", { name: "Edit qty" });
  await field.fill("10");
  await field.press("Tab");
  await expect(grid.locator("[data-r='2'][data-c='3']")).toHaveText("10");
  await expect(grid.locator("[data-active='true']")).toHaveAttribute("data-c", "4");
  await expect(grid.locator("[data-active='true']")).toBeFocused();

  await page.reload();
  await expect(grid.locator("[data-r='2'][data-c='3']")).toHaveText("10");
});

test("Set to NULL saves NULL and the cell shows the NULL token", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "has value", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "has value", exact: true }).dblclick();
  await editor(page, "note").getByRole("button", { name: "Set to NULL" }).click();
  await expect(editor(page, "note").getByRole("textbox", { name: "Edit note" })).toHaveValue("");
  await editor(page, "note").getByRole("button", { name: "Save" }).click();

  await expect(grid.locator("[data-r='6'][data-c='4'] .token-null")).toBeVisible();
  await page.reload();
  await expect(grid.locator("[data-r='6'][data-c='4'] .token-null")).toBeVisible();
});

test("an invalid value stays in the editor with a message; the server's refusal rolls the cell back", async ({
  page,
  writeStudio,
}) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  // Client check: a number column.
  await grid.locator("[data-r='0'][data-c='3']").dblclick();
  const qty = editor(page, "qty").getByRole("textbox", { name: "Edit qty" });
  await qty.fill("many");
  await qty.press("Enter");
  await expect(editor(page, "qty").getByRole("alert")).toHaveText("Enter a number.");
  await qty.press("Escape");
  await expect(grid.locator("[data-r='0'][data-c='3']")).toHaveText("5");

  // The server refuses a date it cannot read; the cell goes back to what it held.
  await grid.locator("[data-r='0'][data-c='7']").dblclick();
  const born = editor(page, "born").getByRole("textbox", { name: "Edit born" });
  await born.fill("tomorrow");
  await born.press("Enter");
  await expect(editor(page, "born").getByRole("alert")).toContainText("born");
  await expect(grid.locator("[data-r='0'][data-c='7']")).toHaveText("2026-03-04T00:00:00Z");
  await born.press("Escape");
  await expect(editor(page, "born")).toBeHidden();
});

test("a concurrent change gives the conflict dialog; Keep mine saves over it", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "conflict-me", exact: true })).toBeVisible();

  await grid.locator("[data-r='4'][data-c='4']").dblclick();
  const field = editor(page, "note").getByRole("textbox", { name: "Edit note" });
  await expect(field).toHaveValue("before");

  // Someone else changes the row after the editor read it.
  const other = await page.request.patch(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows/5`, {
    data: { fields: { note: { text: "theirs", null: false } } },
  });
  expect(other.status()).toBe(200);

  await field.fill("mine");
  await field.press("Enter");

  const dialog = page.getByRole("dialog", { name: "This value changed in the database" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("theirs", { exact: true })).toBeVisible();
  await expect(dialog.getByText("mine", { exact: true })).toBeVisible();
  // Nothing was written yet.
  const row = await (await page.request.get(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows/5`)).json();
  expect((row as { cells: { t: string }[] }).cells[3]?.t).toBe("theirs");

  await dialog.getByRole("button", { name: "Keep mine" }).click();
  await expect(grid.locator("[data-r='4'][data-c='4']")).toHaveText("mine");
  await expect(dialog).toBeHidden();
  const after = await (await page.request.get(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows/5`)).json();
  expect((after as { cells: { t: string }[] }).cells[3]?.t).toBe("mine");
});

test("a concurrent change: Take theirs keeps the database value", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "third", exact: true })).toBeVisible();

  await grid.locator("[data-r='2'][data-c='4']").dblclick();
  const field = editor(page, "note").getByRole("textbox", { name: "Edit note" });
  await expect(field).toHaveValue("third");
  await page.request.patch(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows/3`, {
    data: { fields: { note: { text: "database", null: false } } },
  });

  await field.fill("typed");
  await field.press("Enter");
  const dialog = page.getByRole("dialog", { name: "This value changed in the database" });
  await dialog.getByRole("button", { name: "Take theirs" }).click();

  await expect(grid.locator("[data-r='2'][data-c='4']")).toHaveText("database");
  await page.reload();
  await expect(grid.locator("[data-r='2'][data-c='4']")).toHaveText("database");
});

test("a cut cell reads its full value before the editor opens", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "events?f=id%3Aeq%3A1000");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: /^kind-0/ })).toBeVisible();

  await grid.locator("[data-r='0'][data-c='7']").dblclick();
  const field = editor(page, "note").getByRole("textbox", { name: "Edit note" });
  await expect(field).toHaveValue(/^0{296}1000$/);
  await field.press("Control+Enter");
  // Unchanged: nothing is sent.
  await expect(editor(page, "note")).toBeHidden();
});

test("primary key, binary and oversized cells say why they are not edited", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.locator("[data-r='0'][data-c='1']").dblclick();
  await expect(page.getByTestId("grid-live")).toContainText("primary key");
  await expect(page.getByRole("group", { name: /Editor for/ })).toBeHidden();

  await grid.locator("[data-r='0'][data-c='9']").dblclick();
  await expect(page.getByTestId("grid-live")).toContainText("Binary values cannot be edited");

  await grid.locator("[data-r='7'][data-c='4']").dblclick();
  await expect(page.getByTestId("grid-live")).toContainText("64 KiB");
  await expect(page.getByRole("group", { name: /Editor for/ })).toBeHidden();
});

test("New row adds a row with defaults and shows it", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await page.getByRole("button", { name: "New row" }).click();
  const dialog = page.getByRole("dialog", { name: "New row in items" });
  await expect(dialog).toBeVisible();

  // Required column left on default is refused with a message.
  await dialog.getByRole("button", { name: "Add row" }).click();
  await expect(dialog.getByText(/name has no default|Type a value for name/)).toBeVisible();

  await dialog.getByRole("textbox", { name: /^name / }).fill("added");
  await dialog.getByRole("button", { name: "Add row" }).click();

  await expect(dialog).toBeHidden();
  await expect(grid.getByRole("gridcell", { name: "added", exact: true })).toBeVisible();
  await expect(page.getByTestId("grid-live")).toContainText("Row added");
  const rows = await (await page.request.get(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows?filter=name:eq:added`)).json();
  expect((rows as { rows: { t: string }[][] }).rows).toHaveLength(1);
  // The column left on default got the database default (qty 1).
  expect((rows as { rows: { t: string }[][] }).rows[0]?.[2]?.t).toBe("1");
});

test("the n key opens the new row form in write mode", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "users");
  await expect(page.getByRole("grid", { name: "users rows" }).getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();
  await page.getByRole("grid", { name: "users rows" }).getByRole("gridcell", { name: "ada", exact: true }).click();
  await page.keyboard.press("n");
  await expect(page.getByRole("dialog", { name: "New row in users" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("delete asks for the key to be typed and then removes the row", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "delete-me", exact: true })).toBeVisible();

  await grid.getByRole("button", { name: "Actions for row 6" }).click();
  await page.getByRole("menuitem", { name: "Delete row…" }).click();

  const dialog = page.getByRole("dialog", { name: "Delete this row?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId("delete-key")).toHaveText("6");
  const confirm = dialog.getByRole("button", { name: "Delete row" });
  await expect(confirm).toBeDisabled();

  await dialog.getByLabel("Type the key to confirm").fill("7");
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel("Type the key to confirm").fill("6");
  await expect(confirm).toBeEnabled();
  await confirm.click();

  await expect(grid.getByRole("gridcell", { name: "delete-me", exact: true })).toHaveCount(0);
  await expect(page.getByTestId("grid-live")).toContainText("Row deleted");
  const gone = await page.request.get(`${writeStudio.origin}/api/v1/sources/db/tables/items/rows/6`);
  expect(gone.status()).toBe(404);
});

test("the server refuses a delete without the typed key", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "users");
  const res = await page.request.delete(`${writeStudio.origin}/api/v1/sources/db/tables/users/rows/2`, {
    data: { confirm: "wrong" },
  });
  expect(res.status()).toBe(422);
  const still = await page.request.get(`${writeStudio.origin}/api/v1/sources/db/tables/users/rows/2`);
  expect(still.status()).toBe(200);
});
