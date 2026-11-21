import type { Page } from "@playwright/test";

/**
 * Every interactive element of the page, as the browser exposes it to
 * assistive technology, must have a name. The check runs in the page on the
 * elements that can take an action; the name is read with the rules of the
 * accessible name computation by Playwright's role locators.
 */
export async function unnamedInteractive(page: Page): Promise<string[]> {
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
