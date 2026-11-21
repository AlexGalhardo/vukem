import { maxTabs } from "./tabsState";
import type { Tab, TabsState } from "./tabsState";

/**
 * Open tabs and the text of each query live in sessionStorage, per source: they
 * survive a reload and go when the browser tab closes. Only what a tab is made
 * of is written. Results are not in a tab, and nothing here reads them.
 */

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

const version = 1;
export const maxTextBytes = 64 << 10;

export function tabsKey(source: string): string {
  return `vukem.studio.tabs.${source}`;
}

interface Stored {
  v: number;
  active: string | null;
  seq: number;
  tabs: { id: string; kind: string; title: string; pinned: boolean; text: string; savedText: string; table?: string }[];
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function readTab(v: unknown): Tab | null {
  if (!isRecord(v)) {
    return null;
  }
  const { id, kind, title, pinned, text, savedText, table } = v;
  if (typeof id !== "string" || id === "" || typeof title !== "string" || typeof pinned !== "boolean") {
    return null;
  }
  if (kind === "table") {
    return typeof table === "string" && table !== "" ? { id, kind, title, pinned, text: "", savedText: "", table } : null;
  }
  if (kind === "query" && typeof text === "string" && typeof savedText === "string" && text.length <= maxTextBytes) {
    return { id, kind, title, pinned, text, savedText: savedText.length <= maxTextBytes ? savedText : "" };
  }
  return null;
}

/** parseTabs reads what saveTabs wrote and refuses anything else, so a stale or edited value cannot break the page. */
export function parseTabs(raw: string | null): TabsState | null {
  if (raw === null) {
    return null;
  }
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!isRecord(data) || data.v !== version || !Array.isArray(data.tabs)) {
    return null;
  }
  const tabs: Tab[] = [];
  const seen = new Set<string>();
  for (const item of data.tabs) {
    const tab = readTab(item);
    if (tab && !seen.has(tab.id) && tabs.length < maxTabs) {
      seen.add(tab.id);
      tabs.push(tab);
    }
  }
  if (tabs.length === 0) {
    return null;
  }
  // Pinned tabs first, as the reducer keeps them.
  const ordered = [...tabs.filter((t) => t.pinned), ...tabs.filter((t) => !t.pinned)];
  const active = typeof data.active === "string" && seen.has(data.active) ? data.active : (ordered[0] as Tab).id;
  const seq = typeof data.seq === "number" && Number.isInteger(data.seq) && data.seq >= 0 ? data.seq : ordered.length;
  return { tabs: ordered, active, seq };
}

export function loadTabs(storage: StorageLike, source: string): TabsState | null {
  try {
    return parseTabs(storage.getItem(tabsKey(source)));
  } catch {
    return null;
  }
}

/** serializeTabs lists the fields of a tab one by one: a field added to Tab later is not persisted by accident. */
export function serializeTabs(state: TabsState): string {
  const stored: Stored = {
    v: version,
    active: state.active,
    seq: state.seq,
    tabs: state.tabs.map((t) => ({
      id: t.id,
      kind: t.kind,
      title: t.title,
      pinned: t.pinned,
      text: t.kind === "query" ? t.text : "",
      savedText: t.kind === "query" ? t.savedText : "",
      ...(t.table !== undefined ? { table: t.table } : {}),
    })),
  };
  return JSON.stringify(stored);
}

export function saveTabs(storage: StorageLike, source: string, state: TabsState): void {
  try {
    storage.setItem(tabsKey(source), serializeTabs(state));
  } catch {
    // Storage may be full or blocked: the tabs then last until reload.
  }
}
