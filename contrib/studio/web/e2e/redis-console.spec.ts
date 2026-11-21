import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Services, Studio } from "./fixtures";

const editorName = /Redis command for redis/;

async function openConsole(page: Page, studio: Studio): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/redis/q`);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
}

const editor = (page: Page) => page.getByRole("textbox", { name: editorName });

async function run(page: Page, text: string): Promise<void> {
  await editor(page).fill(text);
  await editor(page).press("Control+Enter");
}

test("a read command runs and its reply shows as text, and as a table", async ({ page, serviceStudio, services }) => {
  await openConsole(page, serviceStudio);
  await run(page, `GET ${services.prefix}str`);

  const reply = page.getByLabel("Reply", { exact: true });
  await expect(reply).toHaveText("hello");

  await run(page, `HGETALL ${services.prefix}hash`);
  await expect(reply).toContainText("a:1");
  await page.getByRole("button", { name: "Table", exact: true }).click();
  const grid = page.getByRole("grid", { name: "Query result" });
  await expect(grid.getByRole("columnheader", { name: /^field/ })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "three", exact: true })).toBeVisible();
});

test("a command outside the allow-list is refused with the same message as before", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);

  for (const line of ["FLUSHALL", 'EVAL "return 1" 0', "CONFIG GET *", "KEYS *", "FUNCTION LIST", "SHUTDOWN"]) {
    await run(page, line);
    await expect(page.getByRole("status").filter({ hasText: /is not a command Studio allows/ })).toBeVisible();
  }

  // What the page does not catch the server refuses, with the message of the old interface.
  const res = await page.request.post(`${serviceStudio.origin}/api/v1/sources/redis/command`, { data: { line: "FLUSHALL" } });
  expect(res.status()).toBe(422);
  expect(((await res.json()) as { error: { message: string } }).error.message).toBe('command "FLUSHALL" is not allowed in vukem-studio');
});

test("a write command is refused without --write, and the server refuses it too", async ({ page, serviceStudio, services }) => {
  await openConsole(page, serviceStudio);
  await run(page, `SET ${services.prefix}new value`);
  await expect(page.getByRole("status").filter({ hasText: "SET writes data" })).toContainText("start it with --write");

  const res = await page.request.post(`${serviceStudio.origin}/api/v1/sources/redis/command`, { data: { line: `SET ${services.prefix}new value` } });
  expect(res.status()).toBe(422);
  expect(((await res.json()) as { error: { message: string } }).error.message).toBe('command "SET" writes data and needs --write');

  await run(page, `EXISTS ${services.prefix}new`);
  await expect(page.getByLabel("Reply", { exact: true })).toHaveText("0");
});

test("completion offers the read commands and no write command without --write", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await editor(page).fill("");
  await editor(page).pressSequentially("hg");
  const list = page.getByRole("listbox", { name: "Completions" });
  await expect(list.getByRole("option", { name: /^HGETALL/ })).toBeVisible();
  await editor(page).press("Tab");
  await expect(editor(page)).toHaveValue("HGET");

  await editor(page).fill("");
  await editor(page).pressSequentially("se");
  await expect(list.getByRole("option", { name: /^SET/ })).toHaveCount(0);
  await expect(list.getByRole("option", { name: /^SCAN/ })).toHaveCount(0);
});

test("the history is shared with the SQL console: a run is kept and can be opened again", async ({ page, serviceStudio, services }) => {
  await openConsole(page, serviceStudio);
  await run(page, `TYPE ${services.prefix}str`);
  await expect(page.getByLabel("Reply", { exact: true })).toHaveText("string");

  await page.getByRole("button", { name: "History and saved queries" }).click();
  await expect(page.getByText(`TYPE ${services.prefix}str`).first()).toBeVisible();
});

test("the console tab and a key tab live side by side", async ({ page, serviceStudio, services }) => {
  await openConsole(page, serviceStudio);
  await page.getByLabel("Key to open in a tab").fill(`${services.prefix}str`);
  await page.getByRole("button", { name: "Open key" }).click();
  await expect(page.getByRole("tab", { name: new RegExp(`${services.prefix}str`) })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Value")).toHaveText("hello");
});
