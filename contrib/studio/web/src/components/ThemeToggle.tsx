import { useState } from "react";
import type { ReactElement } from "react";
import { applyTheme, nextTheme, saveTheme, storedTheme, systemTheme } from "../lib/theme";
import type { Theme } from "../lib/theme";
import { MoonIcon, SunIcon } from "./icons";

function initialTheme(): Theme {
  return storedTheme(window.localStorage) ?? systemTheme((q) => window.matchMedia(q).matches);
}

/** ThemeToggle switches between the light and dark palettes and remembers the choice. */
export function ThemeToggle(): ReactElement {
  const [theme, setTheme] = useState<Theme>(initialTheme);

  const toggle = (): void => {
    const next = nextTheme(theme);
    applyTheme(document.documentElement, next);
    saveTheme(window.localStorage, next);
    setTheme(next);
  };

  return (
    <button type="button" className="icon-button" onClick={toggle} aria-label={`Switch to ${nextTheme(theme)} theme`}>
      {theme === "dark" ? <SunIcon /> : <MoonIcon />}
    </button>
  );
}
