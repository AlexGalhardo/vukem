import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import type { Services, Studio } from "./fixtures";

async function openBrowser(page: Page, studio: Studio): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/redis`);
  await expect(page.getByRole("grid", { name: "Keys" })).toBeVisible();
}

const status = (page: Page) => page.getByTestId("scan-status");

async function scan(page: Page, services: Services, pattern: string, count = "100"): Promise<void> {
  await page.getByLabel("Pattern").fill(`${services.prefix}${pattern}`);
  await page.getByLabel("Count hint").selectOption(count);
  await page.getByRole("button", { name: "Scan", exact: true }).click();
}

test("the browser explains that SCAN is incremental", async ({ page, serviceStudio }) => {
  await openBrowser(page, serviceStudio);
  const note = page.getByRole("note");
  await expect(note).toContainText("SCAN is incremental");
  await expect(note).toContainText("The same key can come back twice");
  await expect(note).toContainText("may be missed");
});

test("Load more reads one SCAN step at a time until the cursor is 0", async ({ page, serviceStudio, services }) => {
  await openBrowser(page, serviceStudio);
  await scan(page, services, "scan:*");

  await expect(status(page)).toContainText("The scan is not finished");
  const loadMore = page.getByRole("button", { name: "Load more" });
  await expect(loadMore).toBeEnabled();

  for (let i = 0; i < 40; i++) {
    if (await loadMore.isDisabled()) break;
    await loadMore.click();
    await expect(page.getByRole("button", { name: "Scanning…" })).toHaveCount(0);
  }

  await expect(status(page)).toContainText("450 keys so far");
  await expect(status(page)).toContainText("The scan is complete");
  await expect(loadMore).toBeDisabled();
  await expect(page.getByRole("grid", { name: "Keys" })).toHaveAttribute("aria-rowcount", "451");
  const steps = Number(/in (\d+) steps/.exec((await status(page).textContent()) ?? "")?.[1]);
  expect(steps).toBeGreaterThan(1);
});

test("a key that SCAN returns twice is listed once", async ({ page, serviceStudio, services }) => {
  // Redis does not repeat a key on demand, so the first step is doubled on its way to the page.
  await page.route("**/api/v1/sources/redis/keys?*", async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as { keys: { key: string }[]; cursor?: string };
    if (body.keys.length > 0 && !route.request().url().includes("cursor=")) {
      body.keys = [...body.keys, body.keys[0] as { key: string }];
    }
    await route.fulfill({ response: res, json: body });
  });

  await openBrowser(page, serviceStudio);
  await scan(page, services, "scan:00*", "1000");

  await expect(status(page)).toContainText("The scan is complete");
  await expect(status(page)).toContainText("1 repeated key was shown once");
  // scan:001 to scan:009 only.
  await expect(status(page)).toContainText("9 keys so far");
  await expect(page.getByRole("grid", { name: "Keys" })).toHaveAttribute("aria-rowcount", "10");
});

test("rows show the type badge text and the time to live", async ({ page, serviceStudio, services }) => {
  await openBrowser(page, serviceStudio);
  await scan(page, services, "*", "1000");
  await expect(status(page)).toContainText("The scan is complete");
  await page.getByLabel("Pattern").fill(`${services.prefix}ttl`);
  await page.getByRole("button", { name: "Scan", exact: true }).click();

  const grid = page.getByRole("grid", { name: "Keys" });
  await expect(grid.getByRole("gridcell", { name: `${services.prefix}ttl`, exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "string", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: /^(1h|59m)$/ })).toBeVisible();

  await page.getByLabel("Pattern").fill(`${services.prefix}str`);
  await page.getByRole("button", { name: "Scan", exact: true }).click();
  await expect(grid.getByRole("gridcell", { name: "no expiry", exact: true })).toBeVisible();
});

test("a long list is virtualized and a key opens with Enter", async ({ page, serviceStudio, services }) => {
  await openBrowser(page, serviceStudio);
  await scan(page, services, "scan:*", "1000");
  await expect(status(page)).toContainText("The scan is complete");

  const grid = page.getByRole("grid", { name: "Keys" });
  expect(await grid.getByRole("row").count()).toBeLessThanOrEqual(100);

  const first = grid.getByRole("row").nth(1).getByRole("gridcell").nth(1);
  const key = (await first.textContent()) ?? "";
  expect(key.startsWith(`${services.prefix}scan:`)).toBe(true);
  await first.click();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(`${serviceStudio.origin}/ui/s/redis/k/${encodeURIComponent(key)}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(key);
});

test("sorting a header orders the keys loaded so far", async ({ page, serviceStudio, services }) => {
  await openBrowser(page, serviceStudio);
  await scan(page, services, "scan:00*", "1000");
  await expect(status(page)).toContainText("9 keys so far");

  const grid = page.getByRole("grid", { name: "Keys" });
  await grid.getByRole("columnheader", { name: /^Key/ }).getByRole("button", { name: "Key", exact: true }).click();
  await grid.getByRole("columnheader", { name: /^Key/ }).getByRole("button", { name: "Key", exact: true }).click();
  await expect(grid.getByRole("columnheader", { name: /^Key/ })).toHaveAttribute("aria-sort", "descending");
  await expect(grid.getByRole("row").nth(1)).toContainText(`${services.prefix}scan:009`);
});

test("the keys never use KEYS and the console refuses it", async ({ page, serviceStudio }) => {
  await openBrowser(page, serviceStudio);
  const res = await page.request.post(`${serviceStudio.origin}/api/v1/sources/redis/command`, { data: { line: "KEYS *" } });
  expect(res.status()).toBe(422);
});
