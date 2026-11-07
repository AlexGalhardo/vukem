import { useEffect, useState } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { IconTooltip } from "@/components/icon-tooltip";
import { t } from "@/lib/ui-text";

const OPTIONS = [
  { value: "light", label: "lightTheme", icon: Sun },
  { value: "dark", label: "darkTheme", icon: Moon },
  { value: "system", label: "systemTheme", icon: Monitor },
] as const;

type Theme = (typeof OPTIONS)[number]["value"];

const STORAGE_KEY = "theme";

function preferredDark(theme: Theme): boolean {
  if (theme === "dark") return true;
  if (theme === "light") return false;
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", preferredDark(theme));
}

export function ThemeToggle() {
  const [theme, setThemeState] = useState<Theme | undefined>(undefined);

  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    const initial: Theme = stored === "light" || stored === "dark" || stored === "system" ? stored : "system";
    setThemeState(initial);
    applyTheme(initial);
    if (initial !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);

  const setTheme = (value: Theme) => {
    localStorage.setItem(STORAGE_KEY, value);
    applyTheme(value);
    setThemeState(value);
  };

  return (
    <div role="radiogroup" aria-label={t.colorTheme} className="flex items-center gap-0.5 rounded-full border border-border p-0.5">
      {OPTIONS.map(({ value, label: key, icon: Icon }) => {
        const label = t[key];
        const active = theme === value;
        return (
          <IconTooltip key={value} label={label}>
            <button
              type="button"
              role="radio"
              aria-checked={active}
              aria-label={label}
              onClick={() => setTheme(value)}
              className={`inline-flex size-6 items-center justify-center rounded-full transition-colors ${
                active ? "bg-hover text-fg" : "text-faint hover:text-fg"
              }`}
            >
              <Icon aria-hidden className="size-3.5" />
            </button>
          </IconTooltip>
        );
      })}
    </div>
  );
}
