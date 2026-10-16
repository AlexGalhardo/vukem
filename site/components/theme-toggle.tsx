"use client";

import { useTheme } from "next-themes";
import { Monitor, Moon, Sun } from "lucide-react";
import { IconTooltip } from "@/components/icon-tooltip";
import { useMounted } from "@/components/use-mounted";

const OPTIONS = [
  { value: "light", label: "Light theme", icon: Sun },
  { value: "dark", label: "Dark theme", icon: Moon },
  { value: "system", label: "System theme", icon: Monitor },
] as const;

export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const mounted = useMounted();
  const current = mounted ? theme : undefined;

  return (
    <div role="radiogroup" aria-label="Color theme" className="flex items-center gap-0.5 rounded-full border border-border p-0.5">
      {OPTIONS.map(({ value, label, icon: Icon }) => {
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
