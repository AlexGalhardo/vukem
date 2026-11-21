import { expect, test } from "./fixtures";

test("read-only Studio: no Write mode switch, and an INSERT is refused with the server's message", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  await page.goto(`${studio.origin}/ui/s/db/q`);
  const editor = page.getByRole("textbox", { name: "SQL for db" });
  await editor.click();

  await expect(page.getByTestId("console-mode")).toHaveText("Read-only");
  await expect(page.getByRole("switch")).toHaveCount(0);
  await expect(page.getByText("Write mode", { exact: true })).toHaveCount(0);

  await page.keyboard.type("insert into users (id, name) values (99, 'zed')");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");

  await expect(page.getByText(/INSERT is not a read/)).toBeVisible();
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("Input error");
  await expect(alert).toContainText("read-only: only SELECT, WITH, SHOW, EXPLAIN, DESCRIBE, TABLE and VALUES");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.keyboard.press("Control+a");
  await page.keyboard.type("select count(*) as n from users where id = 99");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+Enter");
  await expect(page.getByRole("gridcell", { name: "0", exact: true })).toBeVisible();
});

test("read-only Studio: the server refuses a write sent by hand with 403", async ({ page, studio }) => {
  await page.goto(studio.startUrl);
  const status = await page.evaluate(async () => {
    const res = await fetch("/api/v1/sources/db/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sql: "delete from users", write: true }),
    });
    return res.status;
  });
  expect(status).toBe(403);
});
