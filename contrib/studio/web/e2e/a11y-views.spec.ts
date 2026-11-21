import { expect, test } from "./fixtures";
import { unnamedInteractive } from "./names";

test("the schema view names every control and the diagram is a labelled group of links", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/schema`);
  await expect(page.getByTestId("diagram")).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);

  const diagram = page.getByRole("group", { name: "Relationship diagram" });
  await expect(diagram).toHaveAttribute("tabindex", "0");
  await expect(diagram).toHaveAccessibleDescription(/Every table is a link/);
  for (const node of await page.locator("[data-node]").all()) {
    await expect(node).toHaveAttribute("role", "link");
    await expect(node).toHaveAttribute("tabindex", "0");
    expect(((await node.getAttribute("aria-label")) ?? "").length).toBeGreaterThan(0);
  }
});

test("a MongoDB collection, its drawer and its console name every control", async ({ page, serviceStudio }) => {
  await page.goto(serviceStudio.startUrl);
  await page.goto(`${serviceStudio.origin}/ui/s/mongo/c/people`);
  const grid = page.getByRole("grid", { name: "people documents" });
  await expect(grid.getByRole("gridcell", { name: "ada", exact: true })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);

  await grid.getByRole("gridcell", { name: "ada", exact: true }).click();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: /^people 1$/ })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await page.goto(`${serviceStudio.origin}/ui/s/mongo/q`);
  await expect(page.getByRole("textbox", { name: /MongoDB find or aggregate/ })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});

test("the Redis browser, a key and the console name every control", async ({ page, serviceStudio, services }) => {
  await page.goto(serviceStudio.startUrl);
  await page.goto(`${serviceStudio.origin}/ui/s/redis`);
  await expect(page.getByRole("grid", { name: "Keys" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);

  await page.goto(`${serviceStudio.origin}/ui/s/redis/k/${encodeURIComponent(`${services.prefix}bin`)}`);
  await expect(page.getByRole("table", { name: "Hex view" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);

  await page.goto(`${serviceStudio.origin}/ui/s/redis/q`);
  await expect(page.getByRole("textbox", { name: /Redis command/ })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});

test("write mode names the new dialogs of MongoDB and Redis", async ({ page, serviceWriteStudio, services }) => {
  await page.goto(serviceWriteStudio.startUrl);
  await page.goto(`${serviceWriteStudio.origin}/ui/s/mongo/c/events`);
  await page.getByRole("button", { name: "Insert document" }).click();
  await expect(page.getByRole("dialog", { name: "Insert a document into events" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await page.goto(`${serviceWriteStudio.origin}/ui/s/redis/k/${encodeURIComponent(`${services.prefix}str`)}`);
  await page.getByRole("button", { name: "Delete key…" }).click();
  await expect(page.getByRole("dialog", { name: "Delete this key?" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});
