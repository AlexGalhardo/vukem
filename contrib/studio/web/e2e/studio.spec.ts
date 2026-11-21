import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";

// Collects what could break the strict CSP or leave the machine: console
// errors (a blocked script or style is reported there) and every request host.
function watch(page: Page): { errors: string[]; hosts: Set<string> } {
  const errors: string[] = [];
  const hosts = new Set<string>();

  page.on("console", (msg) => {
    if (msg.type() === "error") {
      errors.push(msg.text());
    }
  });
  page.on("pageerror", (err) => errors.push(err.message));
  page.on("request", (req) => {
    const url = new URL(req.url());
    if (url.protocol === "http:" || url.protocol === "https:") {
      hosts.add(url.hostname);
    }
  });

  return { errors, hosts };
}

test("opens the token URL and lists the sources", async ({ page, studio }) => {
  const seen = watch(page);

  await page.goto(studio.startUrl);

  await expect(page).toHaveURL(`${studio.origin}/ui/`);

  await expect(page.getByRole("heading", { name: "Sources" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("link", { name: /^db/ })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Sources" })).toBeVisible();

  expect(seen.errors).toEqual([]);
});

test("the badge says read-only", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/`);

  await expect(page.getByTestId("mode-badge")).toHaveText(/read-only/i);
});

test("the badge says write enabled with --write", async ({ page, writeStudio }) => {
  await page.goto(writeStudio.startUrl);
  await page.goto(`${writeStudio.origin}/ui/`);

  await expect(page.getByTestId("mode-badge")).toHaveText(/write enabled/i);
});

test("opens a source and lists its tables with row estimates", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/`);

  await page.getByRole("main").getByRole("link", { name: /^db/ }).click();

  await expect(page).toHaveURL(`${studio.origin}/ui/s/db`);

  const table = page.getByRole("table", { name: "Tables" });
  await expect(table.getByRole("row", { name: /users\s+2\s+id/ })).toBeVisible();
  await expect(table.getByRole("row", { name: /posts\s+3\s+id/ })).toBeVisible();

  await table.getByRole("link", { name: "posts" }).click();
  await expect(page).toHaveURL(`${studio.origin}/ui/s/db/t/posts`);
  await page.getByText("Columns", { exact: true }).click();
  await expect(page.getByRole("table", { name: "Columns" }).getByRole("row", { name: /user_id\s+integer\s+no/ })).toBeVisible();
});

test("a client route survives a reload", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/t/users`);

  await expect(page.getByRole("heading", { name: "users" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "users" })).toBeVisible();
});

test("no request leaves 127.0.0.1", async ({ page, studio }) => {
  const seen = watch(page);

  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/`);
  await page.getByRole("main").getByRole("link", { name: /^db/ }).click();
  await expect(page.getByRole("table", { name: "Tables" })).toBeVisible();

  expect([...seen.hosts]).toEqual(["127.0.0.1"]);
  expect(seen.errors).toEqual([]);
});

test("the root and the old pages lead to the interface or to 404", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/`);
  await expect(page).toHaveURL(`${studio.origin}/ui/`);
  await expect(page.getByRole("heading", { name: "Sources" })).toBeVisible();

  for (const path of ["/s/db/t/users", "/s/db/console", "/assets/app.js", "/events"]) {
    const res = await page.request.get(`${studio.origin}${path}`);
    expect(res.status()).toBe(404);
  }
});

test("the start token works once", async ({ browser, studio }) => {
  const first = await browser.newContext();
  const ok = await first.newPage();
  await ok.goto(studio.startUrl);
  await ok.goto(`${studio.origin}/ui/`);
  await expect(ok.getByRole("heading", { name: "Sources" })).toBeVisible();

  const second = await browser.newContext();
  const refused = await second.newPage();
  const res = await refused.goto(studio.startUrl);
  expect(res?.status()).toBe(401);

  const api = await second.request.get(`${studio.origin}/api/v1/bootstrap`);
  expect(api.status()).toBe(401);

  await first.close();
  await second.close();
});

test("the theme toggle switches and remembers the palette", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/`);

  const root = page.locator("html");
  const toggle = page.getByRole("button", { name: /^Switch to (light|dark) theme$/ });
  const before = await root.getAttribute("data-theme");

  await toggle.click();
  const after = await root.getAttribute("data-theme");
  expect(after).not.toBeNull();
  expect(after).not.toBe(before);

  await page.reload();
  await expect(root).toHaveAttribute("data-theme", after ?? "");
});

test("the skip link jumps to the main area", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/`);
  await expect(page.getByRole("heading", { name: "Sources" })).toBeVisible();

  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("main")).toBeFocused();
});
