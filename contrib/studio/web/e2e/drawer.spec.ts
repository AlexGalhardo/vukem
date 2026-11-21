import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

async function open(page: Page, studio: { startUrl: string; origin: string }, path: string): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/${path}`);
}

test("opens a row with its values in full and closes with Escape, returning the focus", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("Enter");

  const drawer = page.getByRole("dialog", { name: "items 1" });
  await expect(drawer).toBeVisible();
  await expect(page).toHaveURL(/open=1$/);
  await expect(drawer.getByText("first", { exact: true })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(page).not.toHaveURL(/open=/);
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeFocused();
});

test("shows JSON formatted, with a raw view, and NULL and binary as tokens", async ({ page, studio }) => {
  await open(page, studio, "items?open=1");
  const drawer = page.getByRole("dialog", { name: "items 1" });
  await expect(drawer).toBeVisible();

  await expect(drawer.locator(".drawer-value", { hasText: '"k": 1' })).toBeVisible();
  await drawer.getByRole("button", { name: "Show raw" }).click();
  await expect(drawer.locator(".drawer-value", { hasText: '{"k":1}' })).toBeVisible();
  await expect(drawer.locator(".token-binary")).toHaveText("<binary 2 bytes>");

  await page.goto(`${studio.origin}/ui/s/db/t/items?open=2`);
  await expect(page.getByRole("dialog", { name: "items 2" }).locator(".token-null").first()).toBeVisible();
});

test("a cut cell offers the whole value and the drawer reads it from the row", async ({ page, studio }) => {
  await open(page, studio, "events?f=id%3Aeq%3A1000");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: /^kind-0/ })).toBeVisible();
  await expect(grid.getByRole("button", { name: "Open the full value" })).toBeVisible();

  await grid.getByRole("button", { name: "Open the full value" }).click();
  const drawer = page.getByRole("dialog", { name: "events 1000" });
  await expect(drawer).toBeVisible();
  const note = drawer.locator(".drawer-value").filter({ hasText: /^0+1000$/ });
  await expect(note).toHaveText(/^0{296}1000$/);
});

test("a foreign key links to the referenced row and the drawer lists what refers to a row", async ({ page, studio }) => {
  await open(page, studio, "posts");
  const grid = page.getByRole("grid", { name: "posts rows" });
  await expect(grid.getByRole("gridcell", { name: "first", exact: true })).toBeVisible();

  await grid.getByRole("link", { name: /Open the row of users that user_id points at/ }).first().click();
  await expect(page).toHaveURL(/\/t\/users\?f=id%3Aeq%3A1&open=1$/);
  const drawer = page.getByRole("dialog", { name: "users 1" });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText("ada", { exact: true })).toBeVisible();

  const refs = drawer.getByRole("region", { name: "Referenced by" });
  await expect(refs).toBeVisible();
  await refs.getByRole("link", { name: "posts (user_id)" }).click();
  await expect(page).toHaveURL(/\/t\/posts\?f=user_id%3Aeq%3A1$/);
  await expect(page.getByRole("grid", { name: "posts rows" })).toHaveAttribute("aria-rowcount", "3");
});

test("the row menu and the toolbar open the drawer by keyboard and by mouse", async ({ page, studio }) => {
  await open(page, studio, "users");
  const grid = page.getByRole("grid", { name: "users rows" });
  await expect(grid.getByRole("gridcell", { name: "linus", exact: true })).toBeVisible();

  await grid.getByRole("button", { name: "Actions for row 2" }).click();
  await page.getByRole("menuitem", { name: "Open row" }).click();
  await expect(page.getByRole("dialog", { name: "users 2" })).toBeVisible();
  await page.getByRole("button", { name: "Close the row" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await grid.getByRole("gridcell", { name: "ada", exact: true }).click();
  await page.keyboard.press("Shift+F10");
  await page.getByRole("menuitem", { name: "Open row" }).click();
  await expect(page.getByRole("dialog", { name: "users 1" })).toBeVisible();
});

test("a table without a primary key opens its cells, with a note", async ({ page, studio }) => {
  await open(page, studio, "audit_lines");
  const grid = page.getByRole("grid", { name: "audit_lines rows" });
  await expect(grid.getByRole("gridcell", { name: "two", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "two", exact: true }).click();
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog", { name: "audit_lines row 2" });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText("no primary key")).toBeVisible();
});
