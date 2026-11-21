import { expect, test } from "./fixtures";

interface Frames {
  frames: number;
  worst: number;
  over50: number;
  maxRows: number;
  minRows: number;
}

/**
 * Scrolls the grid by script for a while, one jump per animation frame, and
 * measures the time between frames and how many row elements the DOM holds.
 */
async function scrollAndMeasure(
  page: import("@playwright/test").Page,
  options: { step: number; ms: number },
): Promise<Frames> {
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

          grid.scrollTop += step;
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

test("scrolling 100,000 rows keeps every frame under 50 ms and the DOM under 100 rows", async ({ page, studio }, info) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();
  await expect(grid).toHaveAttribute("aria-rowcount", "100001");

  // Let the first pages settle, then scroll fast: about 19 rows per frame.
  await page.waitForTimeout(300);
  const result = await scrollAndMeasure(page, { step: 600, ms: 3000 });

  info.annotations.push({ type: "perf", description: JSON.stringify(result) });
  console.log(`perf: ${JSON.stringify(result)}`);

  expect(result.frames).toBeGreaterThan(20);
  expect(result.maxRows).toBeLessThanOrEqual(100);
  expect(result.minRows).toBeGreaterThan(10);
  expect(result.worst).toBeLessThanOrEqual(50);
  expect(result.over50).toBe(0);
});

test("the compact row height keeps the DOM windowed and the frames short", async ({ page, studio }, info) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  // The compact height shows more rows per screen: the DOM still stays under 100.
  await page.getByLabel("Row height").selectOption("compact");
  await page.waitForTimeout(200);
  const result = await scrollAndMeasure(page, { step: 900, ms: 1500 });
  info.annotations.push({ type: "perf-compact", description: JSON.stringify(result) });
  expect(result.maxRows).toBeLessThanOrEqual(100);
  expect(result.worst).toBeLessThanOrEqual(50);
});

test("the end of a 100,000-row table is reachable and the scroll size stays below the browser limit", async ({
  page,
  studio,
}) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/events?sort=amount`);
  const grid = page.getByRole("grid", { name: "events rows" });
  await expect(grid.getByRole("gridcell", { name: "kind-1", exact: true }).first()).toBeVisible();

  // A sorted view has random access: scrolling to the bottom loads the last rows at once.
  await grid.evaluate((el) => el.scrollTo({ top: el.scrollHeight }));
  await expect(grid.getByRole("gridcell", { name: "150000", exact: true })).toBeVisible();
  const height = await grid.evaluate((el) => el.scrollHeight);
  expect(height).toBeLessThan(33_000_000);
});
