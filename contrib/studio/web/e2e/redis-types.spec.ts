import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Services, Studio } from "./fixtures";

async function openKey(page: Page, studio: Studio, services: Services, name: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/redis/k/${encodeURIComponent(services.prefix + name)}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(services.prefix + name);
}

test("a string shows its value, its size and its type", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "str");
  await expect(page.getByLabel("Value")).toHaveText("hello");
  await expect(page.getByText("5 bytes.")).toBeVisible();
  await expect(page.locator(".key-type-string")).toHaveText("string");
  await expect(page.getByText("no expiry")).toBeVisible();
});

test("a key with a time to live shows it", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "ttl");
  await expect(page.getByText(/^(1h|59m)$/)).toBeVisible();
});

test("a binary string opens as a hex dump, with a text view", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "bin");

  const hex = page.getByRole("table", { name: "Hex view" });
  await expect(hex).toBeVisible();
  await expect(hex.getByRole("row", { name: /00000000 ff 00 01 61 62 63 \.\.\.abc/ })).toBeVisible();
  await expect(page.getByText("not valid UTF-8")).toBeVisible();

  await page.getByRole("button", { name: "Text", exact: true }).click();
  await expect(hex).toHaveCount(0);
  await expect(page.getByLabel("Value")).toHaveText(String.raw`\xff\x00\x01abc`);
  await page.getByRole("button", { name: "Hex", exact: true }).click();
  await expect(hex).toBeVisible();
});

test("a hash is a grid of fields and values", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "hash");
  const grid = page.getByRole("grid", { name: new RegExp(`${services.prefix}hash hash$`) });
  await expect(grid.getByRole("columnheader", { name: /^Field/ })).toBeVisible();
  await expect(grid.getByRole("columnheader", { name: /^Value/ })).toBeVisible();
  for (const cell of ["a", "b", "c", "three"]) {
    await expect(grid.getByRole("gridcell", { name: cell, exact: true })).toBeVisible();
  }
  await expect(page.getByTestId("key-count")).toHaveText("Showing 3 of 3 elements.");
});

test("a long hash pages with More and shows a visible count", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "hash-big");
  const count = page.getByTestId("key-count");
  await expect(count).toHaveText("Showing 200 of 250 elements.");
  await expect(page.getByText("More elements exist.")).toBeVisible();

  await page.getByRole("button", { name: "Load more" }).click();
  await expect(count).toHaveText("Showing 250 of 250 elements.");
  await expect(page.getByRole("button", { name: "Load more" })).toBeDisabled();
  await expect(page.getByText("That is every element.")).toBeVisible();
});

test("a list has an index and a value", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "list");
  const grid = page.getByRole("grid", { name: /list$/ });
  await expect(grid.getByRole("columnheader", { name: /^Index/ })).toBeVisible();
  for (const v of ["one", "two", "three"]) {
    await expect(grid.getByRole("gridcell", { name: v, exact: true })).toBeVisible();
  }
  await expect(page.getByTestId("key-count")).toHaveText("Showing 3 of 3 elements.");
});

test("a set lists its members", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "set");
  const grid = page.getByRole("grid", { name: /set$/ });
  await expect(grid.getByRole("columnheader", { name: /^Member/ })).toBeVisible();
  for (const v of ["x", "y", "z"]) {
    await expect(grid.getByRole("gridcell", { name: v, exact: true })).toBeVisible();
  }
});

test("a sorted set has a member and a score", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "zset");
  const grid = page.getByRole("grid", { name: /zset$/ });
  await expect(grid.getByRole("columnheader", { name: /^Member/ })).toBeVisible();
  await expect(grid.getByRole("columnheader", { name: /^Score/ })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "low", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "1.5", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "high", exact: true })).toBeVisible();
});

test("a stream has an id and its fields", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "stream");
  const grid = page.getByRole("grid", { name: /stream$/ });
  await expect(grid.getByRole("columnheader", { name: /^Id/ })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "1-0", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "2-0", exact: true })).toBeVisible();
});

test("a row opens its whole value in a dialog, read-only without --write", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "hash");
  const grid = page.getByRole("grid", { name: /hash hash$/ });
  await grid.getByRole("gridcell", { name: "c", exact: true }).click();
  await page.keyboard.press("Enter");

  const dialog = page.getByRole("dialog", { name: "c" });
  await expect(dialog.getByLabel("Value")).toHaveText("three");
  await expect(dialog.getByRole("button", { name: /Edit value/ })).toHaveCount(0);
});

test("a key that does not exist says so", async ({ page, serviceStudio, services }) => {
  await page.goto(serviceStudio.startUrl);
  await page.goto(`${serviceStudio.origin}/ui/s/redis/k/${encodeURIComponent(`${services.prefix}missing`)}`);
  await expect(page.getByRole("alert")).toContainText("does not exist");
});

test("without --write there is no edit, no expiry change and no delete, and the routes do not exist", async ({ page, serviceStudio, services }) => {
  await openKey(page, serviceStudio, services, "str");
  await expect(page.getByRole("button", { name: /Edit value|Expiry|Delete key/ })).toHaveCount(0);

  const key = encodeURIComponent(`${services.prefix}str`);
  const patch = await page.request.patch(`${serviceStudio.origin}/api/v1/sources/redis/keys/${key}`, { data: { action: "set", value: "x" } });
  expect(patch.status()).toBe(404);
  const del = await page.request.delete(`${serviceStudio.origin}/api/v1/sources/redis/keys/${key}`, { data: { confirm: `${services.prefix}str` } });
  expect(del.status()).toBe(404);
  await page.reload();
  await expect(page.getByLabel("Value")).toHaveText("hello");
});
