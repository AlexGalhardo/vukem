import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

interface Frames {
  frames: number;
  worst: number;
  over50: number;
  maxRows: number;
  minRows: number;
}

/**
 * Scrolls the page by script for a while, one jump per animation frame, and
 * measures the time between frames and how many row elements the DOM holds. A
 * paged grid is as tall as its page, so it is the page that scrolls.
 */
async function scrollAndMeasure(page: Page, options: { step: number; ms: number }): Promise<Frames> {
  return page.evaluate(
    ({ step, ms }) =>
      new Promise<Frames>((resolve) => {
        const grid = document.querySelector<HTMLElement>("[role='grid']");
        if (!grid) {
          throw new Error("no grid");
        }
        let last = performance.now();
        const start = last;
        let worst = 0;
        let over50 = 0;
        let frames = 0;
        let maxRows = 0;
        let minRows = Number.MAX_SAFE_INTEGER;

        const tick = (now: number): void => {
          const delta = now - last;
          last = now;
          if (frames > 0) {
            worst = Math.max(worst, delta);
            if (delta > 50) {
              over50++;
            }
          }
          frames++;
          const rows = grid.querySelectorAll("[role='row']").length;
          maxRows = Math.max(maxRows, rows);
          minRows = Math.min(minRows, rows);

          const max = document.documentElement.scrollHeight - window.innerHeight;
          window.scrollTo(0, max > 0 ? (window.scrollY + step) % max : 0);
          if (now - start < ms) {
            requestAnimationFrame(tick);
          } else {
            resolve({ frames, worst, over50, maxRows, minRows });
          }
        };
        requestAnimationFrame(tick);
      }),
    options,
  );
}

async function openLargest(page: Page, studio: { startUrl: string; origin: string }): Promise<void> {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();
  await expect(grid).toHaveAttribute("aria-rowcount", "100001");

  // The largest page: 100 rows and the header row are all in the DOM.
  await page.getByRole("navigation", { name: "Pagination" }).getByLabel("Rows per page").selectOption("100");
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 100 of 100,000");
  await expect(grid.getByRole("row")).toHaveCount(101);
}

test("scrolling a page of 100 rows keeps every frame under 50 ms and the DOM at one page", async ({ page, studio }, info) => {
  await openLargest(page, studio);

  await page.waitForTimeout(300);
  const result = await scrollAndMeasure(page, { step: 600, ms: 3000 });

  info.annotations.push({ type: "perf", description: JSON.stringify(result) });
  console.log(`perf: ${JSON.stringify(result)}`);

  expect(result.frames).toBeGreaterThan(20);
  expect(result.maxRows).toBeLessThanOrEqual(101);
  expect(result.minRows).toBe(101);
  expect(result.worst).toBeLessThanOrEqual(50);
  expect(result.over50).toBe(0);
});

test("the compact row height keeps the page short and the frames short", async ({ page, studio }, info) => {
  await openLargest(page, studio);

  await page.getByLabel("Row height").selectOption("compact");
  await page.waitForTimeout(200);
  const result = await scrollAndMeasure(page, { step: 900, ms: 1500 });
  info.annotations.push({ type: "perf-compact", description: JSON.stringify(result) });
  expect(result.maxRows).toBeLessThanOrEqual(101);
  expect(result.worst).toBeLessThanOrEqual(50);
});

test("turning pages of a 100,000-row table keeps every frame under 50 ms", async ({ page, studio }, info) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events?sort=amount`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();
  await expect(page.getByTestId("grid-position")).toHaveText("Rows 1 to 25 of 100,000");

  const frames = await page.evaluate(
    () =>
      new Promise<{ frames: number; worst: number }>((resolve) => {
        let last = performance.now();
        let worst = 0;
        let frames = 0;
        const next = document.querySelector<HTMLButtonElement>("button[aria-label='Next page']");
        const tick = (now: number): void => {
          if (frames > 0) {
            worst = Math.max(worst, now - last);
          }
          last = now;
          frames++;
          if (frames % 10 === 0) {
            next?.click();
          }
          if (frames < 120) {
            requestAnimationFrame(tick);
          } else {
            resolve({ frames, worst });
          }
        };
        requestAnimationFrame(tick);
      }),
  );
  info.annotations.push({ type: "perf-paging", description: JSON.stringify(frames) });
  expect(frames.worst).toBeLessThanOrEqual(50);
  await expect(page.getByTestId("grid-position")).toHaveText(/^Rows \d[\d,]* to \d[\d,]* of 100,000$/);
});
