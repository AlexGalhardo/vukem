import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Studio } from "./fixtures";

async function openEvents(page: Page, studio: Studio): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/mongo/c/events`);
  await expect(page.getByRole("grid", { name: "events documents" })).toBeVisible();
}

const grid = (page: Page) => page.getByRole("grid", { name: "events documents" });

test("insert adds a document, and invalid JSON cannot be sent", async ({ page, serviceWriteStudio }) => {
  await openEvents(page, serviceWriteStudio);
  await page.getByRole("button", { name: "Insert document" }).click();

  const dialog = page.getByRole("dialog", { name: "Insert a document into events" });
  await dialog.getByLabel("Document (Extended JSON)").fill('{"kind": "inserted-by-test",');
  await expect(dialog.getByRole("alert")).toContainText("Not valid JSON");
  await expect(dialog.getByRole("button", { name: "Insert document" })).toBeDisabled();

  await dialog.getByLabel("Document (Extended JSON)").fill('{"kind": "inserted-by-test", "n": 7}');
  await dialog.getByRole("button", { name: "Insert document" }).click();

  await expect(dialog).toBeHidden();
  await expect(grid(page).getByRole("gridcell", { name: "inserted-by-test", exact: true })).toBeVisible();
});

test("replace edits the whole document and needs the _id typed", async ({ page, serviceWriteStudio }) => {
  await openEvents(page, serviceWriteStudio);
  await grid(page).getByRole("gridcell", { name: "edit-me", exact: true }).click();
  await page.keyboard.press("Enter");

  const drawer = page.getByRole("dialog", { name: "events 65a1b2c3d4e5f60718293a4d" });
  await drawer.getByRole("button", { name: "Edit document" }).click();
  const area = drawer.getByLabel("Document (Extended JSON)");
  await expect(area).toHaveValue(/"note": "before"/);
  await area.fill((await area.inputValue()).replace('"before"', '"after"'));
  await drawer.getByRole("button", { name: "Replace document…" }).click();

  const confirm = page.getByRole("dialog", { name: "Replace this document?" });
  await expect(confirm.getByTestId("confirm-expected")).toHaveText("65a1b2c3d4e5f60718293a4d");
  const go = confirm.getByRole("button", { name: "Replace document" });
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the _id to confirm").fill("65a1b2c3d4e5f60718293a4c");
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the _id to confirm").fill("65a1b2c3d4e5f60718293a4d");
  await go.click();

  await expect(confirm).toBeHidden();
  await expect(drawer.getByRole("list", { name: "Document" })).toContainText("after");
});

test("an invalid replacement stays in the editor", async ({ page, serviceWriteStudio }) => {
  await openEvents(page, serviceWriteStudio);
  await grid(page).getByRole("gridcell", { name: "login", exact: true }).click();
  await page.keyboard.press("Enter");

  const drawer = page.getByRole("dialog", { name: "events 65a1b2c3d4e5f60718293a4b" });
  await drawer.getByRole("button", { name: "Edit document" }).click();
  await drawer.getByLabel("Document (Extended JSON)").fill("[1, 2]");
  await expect(drawer.getByRole("alert")).toContainText("one JSON object");
  await expect(drawer.getByRole("button", { name: "Replace document…" })).toBeDisabled();
});

test("delete needs the _id typed and removes the document", async ({ page, serviceWriteStudio }) => {
  await openEvents(page, serviceWriteStudio);
  await grid(page).getByRole("gridcell", { name: "logout", exact: true }).click();
  await page.keyboard.press("Enter");

  const drawer = page.getByRole("dialog", { name: "events 65a1b2c3d4e5f60718293a4c" });
  await drawer.getByRole("button", { name: "Delete document…" }).click();

  const confirm = page.getByRole("dialog", { name: "Delete this document?" });
  const go = confirm.getByRole("button", { name: "Delete document" });
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the _id to confirm").fill("nope");
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the _id to confirm").fill("65a1b2c3d4e5f60718293a4c");
  await go.click();

  await expect(drawer).toBeHidden();
  await expect(grid(page).getByRole("gridcell", { name: "logout", exact: true })).toHaveCount(0);
  await expect(grid(page).getByRole("gridcell", { name: "login", exact: true })).toBeVisible();
});

test("the server refuses a delete without the typed _id even when asked directly", async ({ page, serviceWriteStudio }) => {
  await openEvents(page, serviceWriteStudio);
  const id = encodeURIComponent('{"$oid":"65a1b2c3d4e5f60718293a4b"}');
  const res = await page.request.delete(`${serviceWriteStudio.origin}/api/v1/sources/mongo/collections/events/documents/${id}`, {
    data: { confirm: "" },
  });
  expect(res.status()).toBe(422);
  await page.reload();
  await expect(grid(page).getByRole("gridcell", { name: "login", exact: true })).toBeVisible();
});

test("an aggregate that writes needs Write mode and the source typed", async ({ page, serviceWriteStudio }) => {
  await page.goto(serviceWriteStudio.startUrl);
  await page.goto(`${serviceWriteStudio.origin}/ui/s/mongo/q`);
  const editor = page.getByRole("textbox", { name: /MongoDB find or aggregate for mongo/ });
  await editor.fill('{"collection": "events", "pipeline": [{"$match": {"kind": "login"}}, {"$out": "copy"}]}');
  await editor.press("Control+Enter");
  await expect(page.getByRole("status").filter({ hasText: "needs Write mode" })).toBeVisible();

  await page.getByRole("switch", { name: "Write mode" }).click();
  await editor.press("Control+Enter");

  const confirm = page.getByRole("dialog", { name: /Run AGGREGATE on mongo/ });
  await expect(confirm).toBeVisible();
  const go = confirm.getByRole("button", { name: "Run AGGREGATE" });
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type mongo to confirm").fill("mongo");
  await go.click();

  await expect(page.getByRole("status").filter({ hasText: /rows in|Done in/ })).toBeVisible();
  await page.goto(`${serviceWriteStudio.origin}/ui/s/mongo`);
  await expect(page.getByRole("table", { name: "Collections" }).getByRole("link", { name: "copy" })).toBeVisible();
});
