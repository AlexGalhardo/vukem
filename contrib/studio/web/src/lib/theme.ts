export type Theme = "light" | "dark";

export const themeKey = "vukem-studio-theme";

interface Store {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** parseTheme accepts only the two stored values; anything else means "follow the system". */
export function parseTheme(value: string | null): Theme | null {
  return value === "light" || value === "dark" ? value : null;
}

/** storedTheme reads the person's choice, or null to follow the system. */
export function storedTheme(store: Store): Theme | null {
  try {
    return parseTheme(store.getItem(themeKey));
  } catch {
    return null;
  }
}

/** systemTheme is what prefers-color-scheme asks for. */
export function systemTheme(matches: (query: string) => boolean): Theme {
  return matches("(prefers-color-scheme: dark)") ? "dark" : "light";
}

/** nextTheme is the theme the toggle switches to. */
export function nextTheme(current: Theme): Theme {
  return current === "dark" ? "light" : "dark";
}

/** saveTheme remembers the choice; storage can be blocked, which is not an error. */
export function saveTheme(store: Store, theme: Theme): void {
  try {
    store.setItem(themeKey, theme);
  } catch {
    // The choice then lasts for this page only.
  }
}

/** applyTheme sets data-theme on the root element, which the tokens follow. */
export function applyTheme(root: { setAttribute(name: string, value: string): void }, theme: Theme): void {
  root.setAttribute("data-theme", theme);
}
