"use client";

import { useTheme } from "next-themes";
import { Monitor, Moon, Sun } from "lucide-react";
import { IconTooltip } from "@/components/icon-tooltip";
import { useMounted } from "@/components/use-mounted";
import { dict, type Locale } from "@/lib/i18n";

const OPTIONS = [
  { value: "light", label: "lightTheme", icon: Sun },
  { value: "dark", label: "darkTheme", icon: Moon },
  { value: "system", label: "systemTheme", icon: Monitor },
] as const;

export function ThemeToggle({ locale }: { locale: Locale }) {
  const t = dict(locale);
  const { theme, setTheme } = useTheme();
  const mounted = useMounted();
  const current = mounted ? theme : undefined;

  return (
    <div role="radiogroup" aria-label={t.colorTheme} className="flex items-center gap-0.5 rounded-full border border-border p-0.5">
      {OPTIONS.map(({ value, label: key, icon: Icon }) => {
        const label = t[key];
        const active = current === value;
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
