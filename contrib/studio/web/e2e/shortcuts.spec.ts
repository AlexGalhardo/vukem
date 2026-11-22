import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, studio: { startUrl: string; origin: string }, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
  await expect(page.getByRole("grid")).toBeVisible();
}

test("/ focuses the filter value and typing there does not trigger shortcuts", async ({ page, studio }) => {
  await open(page, studio, "items");
  const value = page.getByRole("form", { name: "Add a filter" }).getByLabel("Value");

  await page.getByRole("grid").getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("/");
  await expect(value).toBeFocused();
  await expect(value).toHaveValue("");

  // Inside the field every key is text: /, r and ? reach the field and do nothing else.
  await page.keyboard.type("r/?n");
  await expect(value).toHaveValue("r/?n");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("? opens the help, which lists the shortcuts, and Escape closes it", async ({ page, studio }) => {
  await open(page, studio, "items");
  await page.getByRole("grid").getByRole("gridcell", { name: "bolt", exact: true }).click();

  await page.keyboard.press("?");
  const help = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(help).toBeVisible();
  const shortcuts = help.getByRole("table", { name: "Shortcuts" });
  await expect(shortcuts.getByRole("row", { name: /\/ .*filter/i })).toBeVisible();
  await expect(shortcuts.getByRole("row", { name: /r .*refresh/i })).toBeVisible();
  await expect(shortcuts.getByRole("row", { name: /\? .*help/i })).toBeVisible();
  // Read-only: there is no "new row" shortcut to list.
  await expect(shortcuts.getByRole("row", { name: /new row/i })).toHaveCount(0);
  await expect(help.getByRole("table", { name: "Grid keys" })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(help).toBeHidden();
});

test("the toolbar button opens the same help", async ({ page, studio }) => {
  await open(page, studio, "items");
  await page.getByRole("button", { name: "Keyboard shortcuts" }).click();
  await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
  await page.getByRole("button", { name: "Close the shortcut help" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("r refreshes the rows on screen, but not while a select has the focus", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid");
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();

  const rowsRequest = page.waitForRequest((r) => /\/tables\/items\/rows\?/.test(r.url()));
  await page.keyboard.press("r");
  await rowsRequest;
  await expect(page.getByTestId("grid-live")).toContainText("Refreshing");

  let requests = 0;
  page.on("request", (r) => {
    if (/\/tables\/items\/rows\?/.test(r.url())) {
      requests++;
    }
  });
  await page.getByRole("form", { name: "Add a filter" }).getByLabel("Column").focus();
  await page.keyboard.press("r");
  await page.waitForTimeout(300);
  expect(requests).toBe(0);
});

test("the shortcuts are not browser combinations: Ctrl+R is left to the browser", async ({ page, studio }) => {
  await open(page, studio, "items");
  await page.getByRole("grid").getByRole("gridcell", { name: "bolt", exact: true }).click();

  let requests = 0;
  page.on("request", (r) => {
    if (/\/tables\/items\/rows\?/.test(r.url())) {
      requests++;
    }
  });
  await page.keyboard.press("Control+r");
  await page.waitForTimeout(200);
  expect(requests).toBe(0);
});

test("the Shortcuts link next to the keyboard icon shows its key and opens the help, as the key does", async ({ page, studio }) => {
  await open(page, studio, "items");
  const link = page.getByRole("button", { name: /^Shortcuts (Ctrl K|⌘ K)$/ });
  await expect(link).toBeVisible();
  const hint = (await link.locator("kbd").innerText()).trim();
  expect(["Ctrl K", "⌘ K"]).toContain(hint);

  // The link sits to the right of the keyboard icon.
  const icon = await page.getByRole("button", { name: "Keyboard shortcuts" }).boundingBox();
  const box = await link.boundingBox();
  expect((box?.x ?? 0) > (icon?.x ?? 0) + (icon?.width ?? 0) - 1).toBe(true);

  await link.focus();
  await page.keyboard.press("Enter");
  const help = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(help).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(help).toBeHidden();

  // The key it advertises does the same, also while the focus is in a field.
  const chord = hint === "Ctrl K" ? "Control+k" : "Meta+k";
  await page.getByRole("form", { name: "Add a filter" }).getByLabel("Value").focus();
  await page.keyboard.press(chord);
  await expect(help).toBeVisible();
  await expect(help.getByRole("table", { name: "Shortcuts" }).getByRole("row", { name: /(Ctrl|⌘)\+K .*help/i })).toBeVisible();
});

test("the Shortcuts link fits a narrow screen", async ({ page, studio }) => {
  await page.setViewportSize({ width: 360, height: 700 });
  await open(page, studio, "items");
  const link = page.getByRole("button", { name: /^Shortcuts/ });
  await expect(link).toBeVisible();
  const box = await link.boundingBox();
  expect((box?.x ?? 0) + (box?.width ?? 0) <= 360).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
