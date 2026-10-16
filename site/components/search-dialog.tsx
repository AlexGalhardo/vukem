"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Dialog } from "@base-ui/react/dialog";
import { CornerDownLeft, FileText, Hash, Search } from "lucide-react";
import type { SearchEntry } from "@/lib/search";
import { useIsMac } from "@/components/use-mounted";

type Result = SearchEntry & { score: number };

const MAX_RESULTS = 24;

/** Characters of `needle` appear in order in `hay` (fuzzy fallback). */
function subsequence(needle: string, hay: string): boolean {
  let i = 0;
  for (const ch of hay) if (ch === needle[i] && ++i === needle.length) return true;
  return needle.length === 0;
}

function score(entry: SearchEntry, tokens: string[], raw: string, fuzzy: boolean): number {
  const title = entry.title.toLowerCase();
  const heading = (entry.heading ?? "").toLowerCase();
  const text = entry.text.toLowerCase();
  let total = 0;

  for (const t of tokens) {
    let s = 0;
    if (heading.startsWith(t) || title.startsWith(t)) s += 12;
    if (heading.includes(t)) s += 8;
    if (title.includes(t)) s += entry.heading ? 3 : 10;
    if (text.includes(t)) s += 2;
    if (s === 0 && fuzzy && t.length > 2 && subsequence(t, entry.heading ? heading : title)) s += 1;
    if (s === 0) return 0;
    total += s;
  }

  if ((entry.heading ?? entry.title).toLowerCase() === raw) total += 20;
  return total;
}

function snippet(text: string, tokens: string[]): string {
  const lower = text.toLowerCase();
  const at = tokens.map((t) => lower.indexOf(t)).filter((i) => i >= 0).sort((a, b) => a - b)[0] ?? 0;
  const start = Math.max(0, at - 40);
  return (start > 0 ? "…" : "") + text.slice(start, start + 140) + (start + 140 < text.length ? "…" : "");
}

function Highlight({ text, tokens }: { text: string; tokens: string[] }) {
  if (tokens.length === 0) return <>{text}</>;
  const re = new RegExp(`(${tokens.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})`, "gi");
  return (
    <>
      {text.split(re).map((part, i) =>
        i % 2 === 1 ? (
          <mark key={i} className="rounded-sm bg-transparent text-accent">
            {part}
          </mark>
        ) : (
          part
        ),
      )}
    </>
  );
}

export function SearchDialog() {
  const router = useRouter();
  const isMac = useIsMac();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState<SearchEntry[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  const load = useCallback(() => {
    if (index || failed) return;
    fetch("/search-index.json")
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((data: SearchEntry[]) => setIndex(data))
      .catch(() => setFailed(true));
  }, [index, failed]);

  const onOpenChange = useCallback(
    (next: boolean) => {
      setOpen(next);
      if (next) load();
      else {
        setQuery("");
        setActive(0);
      }
    },
    [load],
  );

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        onOpenChange(!open);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onOpenChange]);

  const raw = query.trim().toLowerCase();
  const tokens = useMemo(() => raw.split(/\s+/).filter(Boolean), [raw]);

  const results = useMemo<Result[]>(() => {
    if (!index) return [];
    if (tokens.length === 0) {
      return index.filter((e) => !e.heading).map((e) => ({ ...e, score: 0 }));
    }
    // Exact substring matches first; subsequence (fuzzy) matching only as a fallback.
    const rank = (fuzzy: boolean) =>
      index
        .map((e) => ({ ...e, score: score(e, tokens, raw, fuzzy) }))
        .filter((r) => r.score > 0)
        .sort((a, b) => b.score - a.score)
        .slice(0, MAX_RESULTS);
    const exact = rank(false);
    return exact.length > 0 ? exact : rank(true);
  }, [index, tokens, raw]);

  // Group by category, keeping the best-ranked category first.
  const groups = useMemo(() => {
    const map = new Map<string, Result[]>();
    for (const r of results) map.set(r.category, [...(map.get(r.category) ?? []), r]);
    return [...map.entries()];
  }, [results]);
  const flat = useMemo(() => groups.flatMap(([, items]) => items), [groups]);
  const current = Math.min(active, Math.max(flat.length - 1, 0));

  const go = (r: Result | undefined) => {
    if (!r) return;
    onOpenChange(false);
    router.push(r.href);
  };

  const onInputKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const n = flat.length;
      if (n === 0) return;
      const next = (current + (e.key === "ArrowDown" ? 1 : -1) + n) % n;
      setActive(next);
      listRef.current?.querySelector(`[data-index="${next}"]`)?.scrollIntoView({ block: "nearest" });
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(flat[current]);
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger
        aria-label="Search documentation"
        className="flex h-8 items-center gap-2 rounded-lg text-sm text-faint transition-colors hover:text-fg max-sm:w-8 max-sm:justify-center sm:w-64 sm:border sm:border-border sm:bg-subtle sm:pr-1.5 sm:pl-2.5 sm:hover:border-border-strong"
      >
        <Search aria-hidden className="size-4 shrink-0" />
        <span className="hidden flex-1 text-left sm:inline">Search…</span>
        <kbd className="hidden rounded border border-border bg-bg px-1.5 py-0.5 font-sans text-[11px] font-medium text-faint sm:inline">
          {isMac ? "⌘K" : "Ctrl K"}
        </kbd>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/25 backdrop-blur-[2px] transition-opacity data-ending-style:opacity-0 data-starting-style:opacity-0 dark:bg-black/60" />
        <Dialog.Popup
          initialFocus={() => document.getElementById("docs-search-input")}
          className="fixed top-[12vh] left-1/2 z-50 flex max-h-[70vh] w-[min(40rem,calc(100vw-2rem))] -translate-x-1/2 flex-col overflow-hidden rounded-2xl border border-border bg-bg shadow-2xl shadow-black/10 transition-all duration-150 data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0"
        >
          <Dialog.Title className="sr-only">Search documentation</Dialog.Title>
          <div className="flex items-center gap-3 border-b border-border px-4">
            <Search aria-hidden className="size-4 shrink-0 text-faint" />
            <input
              id="docs-search-input"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={onInputKey}
              placeholder="Search the docs…"
              autoComplete="off"
              spellCheck={false}
              role="combobox"
              aria-expanded
              aria-controls="docs-search-results"
              aria-activedescendant={flat[current] ? `search-result-${current}` : undefined}
              className="h-12 flex-1 bg-transparent text-[15px] text-fg outline-none placeholder:text-faint focus-visible:outline-none"
            />
            <kbd className="rounded border border-border px-1.5 py-0.5 text-[11px] text-faint">Esc</kbd>
          </div>

          <div ref={listRef} id="docs-search-results" role="listbox" aria-label="Results" className="flex-1 overflow-y-auto p-2">
            {failed ? (
              <p className="px-3 py-8 text-center text-sm text-muted">Search index could not be loaded.</p>
            ) : !index ? (
              <p className="px-3 py-8 text-center text-sm text-muted">Loading…</p>
            ) : flat.length === 0 ? (
              <p className="px-3 py-8 text-center text-sm text-muted">No results for “{query}”.</p>
            ) : (
              groups.map(([category, items]) => (
                <div key={category} role="group" aria-label={category} className="mb-2 last:mb-0">
                  <p className="px-3 pt-2 pb-1 text-xs font-medium text-faint">{category}</p>
                  {items.map((r) => {
                    const idx = flat.indexOf(r);
                    const selected = idx === current;
                    const Icon = r.heading ? Hash : FileText;
                    return (
                      <div
                        key={r.href}
                        id={`search-result-${idx}`}
                        data-index={idx}
                        role="option"
                        aria-selected={selected}
                        onMouseMove={() => setActive(idx)}
                        onClick={() => go(r)}
                        className={`flex cursor-pointer items-start gap-3 rounded-lg px-3 py-2.5 ${selected ? "bg-hover" : ""}`}
                      >
                        <Icon aria-hidden className="mt-0.5 size-4 shrink-0 text-faint" />
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm font-medium text-fg">
                            {r.heading ? (
                              <>
                                <span className="text-muted">{r.title} › </span>
                                <Highlight text={r.heading} tokens={tokens} />
                              </>
                            ) : (
                              <Highlight text={r.title} tokens={tokens} />
                            )}
                          </p>
                          {r.text ? (
                            <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-muted">
                              <Highlight text={snippet(r.text, tokens)} tokens={tokens} />
                            </p>
                          ) : null}
                        </div>
                        {selected ? <CornerDownLeft aria-hidden className="mt-0.5 size-3.5 shrink-0 text-faint" /> : null}
                      </div>
                    );
                  })}
                </div>
              ))
            )}
          </div>

          <div className="flex items-center gap-4 border-t border-border px-4 py-2 text-[11px] text-faint">
            <span>
              <kbd className="font-sans">↑</kbd> <kbd className="font-sans">↓</kbd> to navigate
            </span>
            <span>
              <kbd className="font-sans">Enter</kbd> to select
            </span>
            <span>
              <kbd className="font-sans">Esc</kbd> to close
            </span>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
