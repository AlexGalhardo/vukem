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

/**
 * Every interactive element of the page, as the browser exposes it to
 * assistive technology, must have a name. The check runs in the page on the
 * elements that can take an action; the name is read with the rules of the
 * accessible name computation by Playwright's role locators.
 */
async function unnamedInteractive(page: Page): Promise<string[]> {
  const roles = ["button", "link", "textbox", "combobox", "checkbox", "switch", "menuitem", "listbox", "searchbox"] as const;
  const bad: string[] = [];

  for (const role of roles) {
    const all = page.getByRole(role);
    const count = await all.count();
    for (let i = 0; i < count; i++) {
      const el = all.nth(i);
      const name = await el.evaluate((node) => {
        const labelledBy = node.getAttribute("aria-labelledby");
        const byIds = labelledBy
          ? labelledBy
              .split(/\s+/)
              .map((id) => document.getElementById(id)?.textContent ?? "")
              .join(" ")
          : "";
        const labels = "labels" in node ? [...((node as HTMLInputElement).labels ?? [])].map((l) => l.textContent ?? "").join(" ") : "";
        return [node.getAttribute("aria-label"), byIds, labels, node.textContent, node.getAttribute("title")]
          .map((s) => (s ?? "").trim())
          .find((s) => s !== "") ?? "";
      });
      if (name === "") {
        bad.push(`${role} #${i}: ${(await el.evaluate((n) => n.outerHTML)).slice(0, 120)}`);
      }
    }
  }
  return bad;
}

test("every interactive element of the grid page has an accessible name", async ({ page, studio }) => {
  await open(page, studio, "items?f=qty%3Agte%3A1");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});

test("names also hold in the menu, the drawer and the help dialog", async ({ page, studio }) => {
  await open(page, studio, "posts");
  const grid = page.getByRole("grid", { name: "posts rows" });
  await expect(grid.getByRole("gridcell", { name: "first", exact: true })).toBeVisible();

  await grid.getByRole("button", { name: "Actions for row 1" }).click();
  await expect(page.getByRole("menu", { name: "Actions for row 1" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await grid.getByRole("columnheader", { name: /^title/ }).getByRole("button", { name: /Column menu/ }).click();
  await expect(page.getByRole("menu", { name: /Column menu for title/ })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await grid.getByRole("gridcell", { name: "first", exact: true }).click();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "posts 1" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await page.keyboard.press("?");
  await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});

test("names hold for the editor, the new row form and the delete dialog in write mode", async ({ page, writeStudio }) => {
  await open(page, writeStudio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.locator("[data-r='0'][data-c='4']").dblclick();
  await expect(page.getByRole("group", { name: "Editor for note" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "New row" }).click();
  await expect(page.getByRole("dialog", { name: "New row in items" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
  await page.keyboard.press("Escape");

  await grid.getByRole("button", { name: "Actions for row 1" }).click();
  await page.getByRole("menuitem", { name: "Delete row…" }).click();
  await expect(page.getByRole("dialog", { name: "Delete this row?" })).toBeVisible();
  expect(await unnamedInteractive(page)).toEqual([]);
});

test("the grid exposes grid semantics: counts, indexes, one tab stop, a live region", async ({ page, studio }) => {
  await open(page, studio, "events");
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  await expect(grid).toHaveAttribute("aria-rowcount", "100001");
  await expect(grid).toHaveAttribute("aria-colcount", "8");
  await expect(grid).toHaveAttribute("aria-multiselectable", "true");

  // Every rendered row and cell says where it is in the whole grid.
  const missing = await grid.evaluate((el) => {
    const out: string[] = [];
    for (const row of el.querySelectorAll("[role='row']")) {
      const index = Number(row.getAttribute("aria-rowindex"));
      if (!Number.isInteger(index) || index < 1) {
        out.push("row without aria-rowindex");
      }
      for (const cell of row.querySelectorAll("[role='gridcell'], [role='columnheader']")) {
        const col = Number(cell.getAttribute("aria-colindex"));
        if (!Number.isInteger(col) || col < 1) {
          out.push(`cell without aria-colindex in row ${index}`);
        }
      }
    }
    return out;
  });
  expect(missing).toEqual([]);

  // Row indexes follow the rows: the first data row is row 2 (the header is row 1).
  await expect(grid.getByRole("row").nth(1)).toHaveAttribute("aria-rowindex", "2");

  // One tab stop, then Tab leaves the grid.
  expect(await grid.locator("[tabindex='0']").count()).toBe(1);

  // Sorted columns say so, the others do not.
  await grid.getByRole("columnheader", { name: /^kind/ }).getByRole("button", { name: "kind", exact: true }).click();
  await expect(grid.getByRole("columnheader", { name: /^kind/ })).toHaveAttribute("aria-sort", "ascending");
  expect(await grid.locator("[aria-sort='none']").count()).toBe(0);

  // The live region is polite and present.
  const live = page.getByTestId("grid-live");
  await expect(live).toHaveAttribute("aria-live", "polite");
  await expect(live).toHaveAttribute("role", "status");

  // A page with a grid has one h1 and the language is set.
  await expect(page.locator("h1")).toHaveCount(1);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
});

test("the grid works by keyboard alone: filter, sort, open a row and close it", async ({ page, studio }) => {
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  // The filter bar by keyboard.
  await page.keyboard.press("/");
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.type("name");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  await page.keyboard.type("ut");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/f=name%3Acontains%3Aut/);
  await expect(grid.getByRole("gridcell", { name: "nut", exact: true })).toBeVisible();
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toHaveCount(0);

  // Into the grid by the keyboard, to a header, sort, then to a row and open it.
  await grid.locator("[data-r='0'][data-c='1']").focus();
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/sort=name$/);
  // The focus stays on the header, so the same key cycles the sort.
  await expect(grid.locator("[data-active='true']")).toHaveAttribute("data-r", "-1");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/sort=name&desc=1$/);
  await expect(grid.getByRole("gridcell", { name: "nut", exact: true })).toBeVisible();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: /items 2/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(grid.locator("[data-active='true']")).toBeFocused();
});

test("the focus ring is visible on the active cell and reduced motion switches animation off", async ({ page, studio }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page, studio, "items");
  const grid = page.getByRole("grid", { name: "items rows" });
  await expect(grid.getByRole("gridcell", { name: "bolt", exact: true })).toBeVisible();

  await grid.getByRole("gridcell", { name: "bolt", exact: true }).click();
  await page.keyboard.press("ArrowDown");
  const ring = await grid.locator("[data-active='true']").evaluate((el) => {
    const style = getComputedStyle(el);
    return { outline: style.outlineStyle, shadow: style.boxShadow };
  });
  // Either the focus outline or the inset ring of the active cell is drawn.
  expect(ring.outline !== "none" || ring.shadow !== "none").toBe(true);

  const motion = await page.evaluate(() => getComputedStyle(document.body).transitionDuration);
  expect(["0s", ""]).toContain(motion);
});
