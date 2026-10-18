"use client";

import { useEffect, useState } from "react";
import { t } from "@/lib/ui-text";
import type { Heading } from "@/lib/toc";

/** "On this page" with scroll-spy over the page's h2/h3 headings. */
export function Toc({ headings }: { headings: Heading[] }) {
  const [active, setActive] = useState<string | null>(null);

  useEffect(() => {
    const els = headings
      .map((h) => document.getElementById(h.id))
      .filter((el): el is HTMLElement => el !== null);
    if (els.length === 0) return;

    const visible = new Map<string, boolean>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) visible.set(e.target.id, e.isIntersecting);
        const firstVisible = els.find((el) => visible.get(el.id));
        if (firstVisible) {
          setActive(firstVisible.id);
          return;
        }
        // Nothing in the band: keep the last heading above the viewport.
        const above = els.filter((el) => el.getBoundingClientRect().top < 120);
        setActive(above.at(-1)?.id ?? els[0].id);
      },
      { rootMargin: "-64px 0px -65% 0px", threshold: [0, 1] },
    );
    els.forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [headings]);

  if (headings.length === 0) return null;

  return (
    <nav aria-label={t.onThisPage} className="text-sm">
      <p className="mb-3 text-xs font-semibold tracking-wide text-fg">{t.onThisPage}</p>
      <ul className="space-y-1 border-l border-border">
        {headings.map((h) => {
          const on = h.id === active;
          return (
            <li key={h.id}>
              <a
                href={`#${h.id}`}
                aria-current={on ? "location" : undefined}
                className={`-ml-px block border-l py-1 leading-snug transition-colors ${h.depth === 3 ? "pl-6" : "pl-3"} ${
                  on ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg"
                }`}
              >
                {h.text}
              </a>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
