import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Services, Studio } from "./fixtures";

async function openKey(page: Page, studio: Studio, services: Services, name: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/redis/k/${encodeURIComponent(services.prefix + name)}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(services.prefix + name);
}

test("editing a string asks for a confirmation and keeps the expiry", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "ttl");
  await page.getByRole("button", { name: "Edit value…" }).click();

  const edit = page.getByRole("dialog", { name: `Edit ${services.prefix}ttl` });
  await edit.getByLabel("Value").fill("later");
  await edit.getByRole("button", { name: "Review the change…" }).click();

  const confirm = page.getByRole("dialog", { name: "Replace this value?" });
  await expect(confirm).toContainText("keeps its expiry");
  await confirm.getByRole("button", { name: "Replace value" }).click();

  await expect(edit).toBeHidden();
  await expect(page.getByLabel("Value", { exact: true })).toHaveText("later");
  // The key is seeded once with one hour to live, before the whole suite.
  await expect(page.getByText(/^(1h|[3-5]\dm)$/)).toBeVisible();
});

test("cancelling the confirmation changes nothing", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "edit");
  await page.getByRole("button", { name: "Edit value…" }).click();
  const edit = page.getByRole("dialog", { name: `Edit ${services.prefix}edit` });
  await edit.getByLabel("Value").fill("changed");
  await edit.getByRole("button", { name: "Review the change…" }).click();
  await page.getByRole("dialog", { name: "Replace this value?" }).getByRole("button", { name: "Cancel" }).click();
  await edit.getByRole("button", { name: "Cancel" }).click();

  await page.reload();
  await expect(page.getByLabel("Value", { exact: true })).toHaveText("before");
});

test("a hash field is edited from its row", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "hash-edit");
  const grid = page.getByRole("grid", { name: /hash-edit hash$/ });
  await grid.getByRole("gridcell", { name: "before", exact: true }).click();
  await page.keyboard.press("Enter");

  await page.getByRole("dialog", { name: "f" }).getByRole("button", { name: "Edit value…" }).click();
  const edit = page.getByRole("dialog", { name: "Edit field f" });
  await edit.getByLabel("Value").fill("after");
  await edit.getByRole("button", { name: "Review the change…" }).click();
  await page.getByRole("dialog", { name: "Replace this field?" }).getByRole("button", { name: "Replace field" }).click();

  await expect(grid.getByRole("gridcell", { name: "after", exact: true })).toBeVisible();
});

test("the expiry is set and removed", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "str");
  await page.getByRole("button", { name: "Expiry…" }).click();

  const dialog = page.getByRole("dialog", { name: "Expiry" });
  await expect(dialog.getByRole("button", { name: "Set the expiry" })).toBeDisabled();
  await dialog.getByLabel("Expire after this many seconds").fill("120");
  await dialog.getByRole("button", { name: "Set the expiry" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/^(2m|1m)$/)).toBeVisible();

  await page.getByRole("button", { name: "Expiry…" }).click();
  await dialog.getByRole("button", { name: "Remove the expiry" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("no expiry")).toBeVisible();
});

test("delete needs the key typed", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "gone");
  await page.getByRole("button", { name: "Delete key…" }).click();

  const confirm = page.getByRole("dialog", { name: "Delete this key?" });
  const go = confirm.getByRole("button", { name: "Delete key" });
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the key to confirm").fill(`${services.prefix}gon`);
  await expect(go).toBeDisabled();
  await confirm.getByLabel("Type the key to confirm").fill(`${services.prefix}gone`);
  await go.click();

  await expect(page).toHaveURL(/\/ui\/s\/redis$/);
  const exists = await page.request.post(`${serviceWriteStudio.origin}/api/v1/sources/redis/command`, { data: { line: `EXISTS ${services.prefix}gone` } });
  expect(((await exists.json()) as { result: string }).result).toBe("0");
});

test("the server refuses a delete whose confirmation is not the key", async ({ page, serviceWriteStudio, services }) => {
  await openKey(page, serviceWriteStudio, services, "str");
  const key = encodeURIComponent(`${services.prefix}str`);
  const res = await page.request.delete(`${serviceWriteStudio.origin}/api/v1/sources/redis/keys/${key}`, { data: { confirm: "nope" } });
  expect(res.status()).toBe(422);
  await page.reload();
  await expect(page.getByLabel("Value", { exact: true })).toHaveText("hello");
});

test("the console runs a write command only in Write mode, after a confirmation", async ({ page, serviceWriteStudio, services }) => {
  await page.goto(serviceWriteStudio.startUrl);
  await page.goto(`${serviceWriteStudio.origin}/ui/s/redis/q`);
  const editor = page.getByRole("textbox", { name: /Redis command for redis/ });

  await editor.fill(`SET ${services.prefix}console value`);
  await editor.press("Control+Enter");
  await expect(page.getByRole("status").filter({ hasText: "Turn on Write mode" })).toBeVisible();

  await page.getByRole("switch", { name: "Write mode" }).click();
  await editor.press("Control+Enter");
  const confirm = page.getByRole("dialog", { name: /Run SET on redis/ });
  await expect(confirm).toBeVisible();
  await confirm.getByRole("button", { name: "Run SET" }).click();
  await expect(page.getByLabel("Reply", { exact: true })).toHaveText("OK");

  await editor.fill(`DEL ${services.prefix}console`);
  await editor.press("Control+Enter");
  const del = page.getByRole("dialog", { name: /Run DEL on redis/ });
  await expect(del.getByRole("button", { name: "Run DEL" })).toBeDisabled();
  await del.getByLabel("Type redis to confirm").fill("redis");
  await del.getByRole("button", { name: "Run DEL" }).click();
  await expect(page.getByLabel("Reply", { exact: true })).toHaveText("1");
});
