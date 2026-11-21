import { describe, expect, test } from "bun:test";
import { applyTheme, nextTheme, parseTheme, saveTheme, storedTheme, systemTheme, themeKey } from "./theme";

function memoryStore(initial: Record<string, string> = {}): { getItem(k: string): string | null; setItem(k: string, v: string): void } {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
  };
}

describe("theme", () => {
  test("parseTheme accepts only the two stored values", () => {
    expect(parseTheme("dark")).toBe("dark");
    expect(parseTheme("light")).toBe("light");
    expect(parseTheme("auto")).toBeNull();
    expect(parseTheme(null)).toBeNull();
  });

  test("storedTheme reads the saved choice and survives blocked storage", () => {
    expect(storedTheme(memoryStore({ [themeKey]: "dark" }))).toBe("dark");
    expect(storedTheme(memoryStore())).toBeNull();

    const blocked = {
      getItem: (): string | null => {
        throw new Error("blocked");
      },
      setItem: (): void => {
        throw new Error("blocked");
      },
    };
    expect(storedTheme(blocked)).toBeNull();
    saveTheme(blocked, "dark");
  });

  test("saveTheme remembers the choice", () => {
    const store = memoryStore();
    saveTheme(store, "light");
    expect(store.getItem(themeKey)).toBe("light");
  });

  test("systemTheme follows prefers-color-scheme", () => {
    expect(systemTheme(() => true)).toBe("dark");
    expect(systemTheme(() => false)).toBe("light");
  });

  test("nextTheme flips and applyTheme sets data-theme", () => {
    expect(nextTheme("dark")).toBe("light");
    expect(nextTheme("light")).toBe("dark");

    const attrs: Record<string, string> = {};
    applyTheme({ setAttribute: (n, v) => void (attrs[n] = v) }, "dark");
    expect(attrs["data-theme"]).toBe("dark");
  });
});
