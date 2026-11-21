import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Studio } from "./fixtures";

const editorName = /MongoDB find or aggregate for mongo/;

async function openConsole(page: Page, studio: Studio): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/mongo/q`);
  await expect(page.getByRole("tab", { name: /Query 1/ })).toBeVisible();
}

const editor = (page: Page) => page.getByRole("textbox", { name: editorName });

async function run(page: Page, text: string): Promise<void> {
  await editor(page).fill(text);
  await editor(page).press("Control+Enter");
}

test("a find runs and shows the documents in the result grid", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await run(page, '{"collection": "people", "filter": {"age": {"$gt": 80}}}');

  await expect(page.getByRole("status").filter({ hasText: /rows in/ })).toContainText("18 rows in");
  const grid = page.getByRole("grid", { name: "Query result" });
  await expect(grid.getByRole("columnheader", { name: /^name/ })).toBeVisible();
});

test("an aggregate runs", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await run(page, '{"collection": "people", "pipeline": [{"$match": {"active": true}}, {"$count": "n"}]}');

  const grid = page.getByRole("grid", { name: "Query result" });
  await expect(grid.getByRole("gridcell", { name: "116", exact: true })).toBeVisible();
});

test("text that is not JSON is refused before anything is sent, with its position", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST") sent.push(r.url());
  });

  await run(page, '{"collection": "people",\n "filter": {]}');
  await expect(page.getByRole("status").filter({ hasText: "Not valid JSON" })).toContainText("line 2");
  expect(sent).toEqual([]);

  await run(page, '{"collection": "people"}');
  await expect(page.getByRole("status").filter({ hasText: "exactly one of" })).toBeVisible();
  expect(sent).toEqual([]);
});

test("operators that run JavaScript are refused, and so are $out and $merge without --write", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST") sent.push(r.url());
  });

  for (const [text, message] of [
    ['{"collection": "people", "filter": {"$where": "sleep(1000)"}}', "$where runs JavaScript"],
    ['{"collection": "people", "pipeline": [{"$addFields": {"x": {"$function": {"body": "x", "args": [], "lang": "js"}}}}]}', "$function runs JavaScript"],
    ['{"collection": "people", "pipeline": [{"$group": {"_id": 1, "x": {"$accumulator": {}}}}]}', "$accumulator runs JavaScript"],
    ['{"collection": "people", "pipeline": [{"$match": {}}, {"$out": "copy"}]}', "$out writes data and needs Studio to run with --write"],
    ['{"collection": "people", "pipeline": [{"$merge": {"into": "copy"}}]}', "$merge writes data and needs Studio to run with --write"],
  ] as const) {
    await run(page, text);
    await expect(page.getByRole("status").filter({ hasText: message })).toBeVisible();
  }
  expect(sent).toEqual([]);
});

test("the server refuses what the page did not catch (a script operator sent by hand)", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  const find = await page.request.post(`${serviceStudio.origin}/api/v1/sources/mongo/find`, {
    data: { collection: "people", filter: '{"$where":"1"}' },
  });
  expect(find.status()).toBe(422);
  const out = await page.request.post(`${serviceStudio.origin}/api/v1/sources/mongo/aggregate`, {
    data: { collection: "people", pipeline: '[{"$out":"copy"}]' },
  });
  expect(out.status()).toBe(422);
});

test("completion offers collection names and stage names", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await editor(page).fill("");
  await editor(page).pressSequentially('{"collection": "pe');
  const list = page.getByRole("listbox", { name: "Completions" });
  await expect(list.getByRole("option", { name: /^people/ })).toBeVisible();
  await editor(page).press("Tab");
  await expect(editor(page)).toHaveValue('{"collection": "people');

  await editor(page).fill("");
  await editor(page).pressSequentially('{"collection": "people", "pipeline": [{"$ma');
  await expect(list.getByRole("option", { name: /^\$match/ })).toBeVisible();
  await expect(list.getByRole("option", { name: /^\$where/ })).toHaveCount(0);
});

test("a new console starts from a find on a collection, and brackets are matched", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await page.getByRole("button", { name: "New console" }).click();
  await expect(editor(page)).toHaveValue(/"collection": "events"/);
  await expect(editor(page)).toHaveValue(/"filter": \{\}/);

  await editor(page).fill('{"a": [1, 2]}');
  await editor(page).evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(1, 1));
  await editor(page).press("ArrowLeft");
  await editor(page).press("ArrowRight");
  await expect(page.locator(".sqled-box-bracket")).toHaveCount(2);
});

test("a run is kept in the history of this browser", async ({ page, serviceStudio }) => {
  await openConsole(page, serviceStudio);
  await run(page, '{"collection": "events", "filter": {}}');
  await expect(page.getByRole("status").filter({ hasText: /rows in/ })).toContainText("3 rows in");

  await page.getByRole("button", { name: "History and saved queries" }).click();
  await expect(page.getByText('"collection": "events"').first()).toBeVisible();
});
