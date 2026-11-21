import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

const lines = 5000;

async function open(page: Page, origin: string, startUrl: string): Promise<void> {
  await page.goto(startUrl);
  await page.goto(`${origin}/ui/s/db/q`);
  await page.getByRole("textbox", { name: "SQL for db" }).click();
}

/** Puts a 5,000-line script in the editor the way a paste does: through the value setter and an input event. */
async function fill(page: Page): Promise<void> {
  await page.evaluate((n) => {
    const ta = document.querySelector<HTMLTextAreaElement>("textarea.sqled-input");
    if (!ta) {
      throw new Error("no editor");
    }
    const text = Array.from({ length: n }, (_, i) => `select col_${i}, 'value ${i}', ${i} * 2 from table_${i} where id = ${i}; -- line ${i}`).join("\n");
    const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
    set?.call(ta, text);
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  }, lines);
  await expect(page.locator(".sqled-pos")).toBeVisible();
}

test("typing in a 5,000-line text costs under 16 ms a key", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await fill(page);

  // Only the lines in view are drawn, however long the text.
  expect(await page.locator(".sqled-line").count()).toBeLessThan(80);
  expect(await page.locator(".sqled-num").count()).toBeLessThan(80);

  const stats = await page.evaluate((n) => {
    const ta = document.querySelector<HTMLTextAreaElement>("textarea.sqled-input") as HTMLTextAreaElement;
    ta.focus();
    // The middle of the text, scrolled into view, as a person editing there would have it.
    const mid = ta.value.indexOf("-- line " + Math.floor(n / 2));
    ta.setSelectionRange(mid, mid);
    ta.scrollTop = Math.floor(n / 2) * 20 - 100;

    const times: number[] = [];
    for (let i = 0; i < 80; i++) {
      const t0 = performance.now();
      document.execCommand("insertText", false, i % 7 === 6 ? "\n" : "x");
      // Reading a layout property forces the style and layout work this key caused.
      void document.body.offsetHeight;
      times.push(performance.now() - t0);
    }
    times.sort((a, b) => a - b);
    return {
      median: times[Math.floor(times.length / 2)] as number,
      p95: times[Math.floor(times.length * 0.95)] as number,
      max: times[times.length - 1] as number,
      lines: ta.value.split("\n").length,
    };
  }, lines);

  console.log(`5,000-line typing: median ${stats.median.toFixed(2)} ms, p95 ${stats.p95.toFixed(2)} ms, max ${stats.max.toFixed(2)} ms`);
  expect(stats.lines).toBeGreaterThan(lines);
  expect(stats.p95).toBeLessThan(16);
  expect(stats.median).toBeLessThan(16);
});

test("real key presses cost no more in a 5,000-line text than in an empty one, within the budget", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  const keys = 40;

  // The test driver adds a round trip to every key it sends: measure it on an empty editor first.
  const timeKeys = async (): Promise<number> => {
    const start = Date.now();
    await page.keyboard.type("x".repeat(keys), { delay: 0 });
    return (Date.now() - start) / keys;
  };
  const empty = await timeKeys();

  await fill(page);
  await editor.focus();
  await page.keyboard.press("Control+End");
  const long = await timeKeys();

  console.log(`typing through the keyboard: ${empty.toFixed(2)} ms a key when empty, ${long.toFixed(2)} ms in 5,000 lines (both include the test driver)`);
  // What the long text adds is the cost of the editor itself: it stays inside the 16 ms budget.
  expect(long - empty).toBeLessThan(16);
});

test("scrolling a long text keeps the highlight layer on the lines in view", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await fill(page);
  await page.evaluate(() => {
    const ta = document.querySelector<HTMLTextAreaElement>("textarea.sqled-input") as HTMLTextAreaElement;
    ta.scrollTop = 3000 * 20;
  });
  await expect(page.locator(".sqled-num").filter({ hasText: /^3001$/ })).toBeVisible();
  // The layers share metrics: the line drawn for 3001 sits where the text area puts it.
  const line = await page.locator(".sqled-line", { hasText: "col_3000," }).boundingBox();
  const area = await page.getByRole("textbox", { name: "SQL for db" }).boundingBox();
  expect(line !== null && area !== null && line.y >= area.y - 1 && line.y <= area.y + area.height).toBe(true);
});

test("a tab opens in under 50 ms", async ({ page, studio }) => {
  await open(page, studio.origin, studio.startUrl);
  await fill(page);

  const ms = await page.evaluate(() => {
    const button = [...document.querySelectorAll("button")].find((b) => b.textContent === "New query") as HTMLButtonElement;
    const t0 = performance.now();
    button.click();
    void document.body.offsetHeight;
    return performance.now() - t0;
  });
  console.log(`opening a tab: ${ms.toFixed(2)} ms`);
  await expect(page.getByRole("tab", { name: /Query 2/ })).toHaveAttribute("aria-selected", "true");
  expect(ms).toBeLessThan(50);
});
