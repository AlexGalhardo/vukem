/**
 * The tabs of one source as a pure reducer. A tab is a table or a query. What a
 * tab holds is small and serializable: the text of a query, the name of a
 * table. Results are not part of it, so they can never be persisted by mistake.
 */

export const maxTabs = 20;

/**
 * What a tab shows: a query, a SQL table, the schema of a SQL source, a MongoDB
 * collection or a Redis key. The console of MongoDB and Redis is a query tab.
 */
export type TabKind = "query" | "table" | "schema" | "collection" | "key";

export interface Tab {
  id: string;
  kind: TabKind;
  title: string;
  pinned: boolean;
  /** A query's text. Empty for a table. */
  text: string;
  /** What the text was when it was last saved to the library or loaded from it; the tab is unsaved while text differs. */
  savedText: string;
  /** The name of what a table, collection or key tab shows. A query or schema tab has none. */
  table?: string;
}

export interface TabsState {
  tabs: Tab[];
  active: string | null;
  /** Counts the query tabs ever opened, for their titles. */
  seq: number;
}

export const emptyTabs: TabsState = { tabs: [], active: null, seq: 0 };

export type TabsAction =
  | { type: "openQuery"; id: string; text?: string }
  | { type: "openTable"; id: string; table: string }
  | { type: "openCollection"; id: string; collection: string }
  | { type: "openKey"; id: string; key: string }
  | { type: "openSchema"; id: string }
  | { type: "close"; id: string }
  | { type: "closeOthers"; id: string }
  | { type: "activate"; id: string }
  | { type: "activateIndex"; index: number }
  | { type: "step"; delta: 1 | -1 }
  | { type: "move"; id: string; to: number }
  | { type: "shift"; id: string; delta: 1 | -1 }
  | { type: "pin"; id: string; pinned: boolean }
  | { type: "duplicate"; id: string; newId: string }
  | { type: "setText"; id: string; text: string }
  | { type: "markSaved"; id: string }
  | { type: "rename"; id: string; title: string }
  | { type: "restore"; state: TabsState };

export function canOpen(state: TabsState): boolean {
  return state.tabs.length < maxTabs;
}

export function isUnsaved(tab: Tab): boolean {
  return tab.kind === "query" && tab.text !== tab.savedText;
}

function indexOf(state: TabsState, id: string): number {
  return state.tabs.findIndex((t) => t.id === id);
}

/** Pinned tabs sit first: a tab inserted among them would break that, so unpinned tabs go after the last pinned one. */
function firstUnpinned(tabs: readonly Tab[]): number {
  const i = tabs.findIndex((t) => !t.pinned);
  return i < 0 ? tabs.length : i;
}

function insert(state: TabsState, tab: Tab, at: number): TabsState {
  const tabs = [...state.tabs];
  tabs.splice(at, 0, tab);
  return { ...state, tabs, active: tab.id };
}

function closeAt(state: TabsState, index: number): TabsState {
  const closed = state.tabs[index];
  const tabs = state.tabs.filter((_, i) => i !== index);
  if (!closed) {
    return state;
  }
  let active = state.active;
  if (active === closed.id) {
    // The neighbour on the right takes over, else the one on the left.
    active = (tabs[index] ?? tabs[index - 1])?.id ?? null;
  }
  return { ...state, tabs, active };
}

/** moveTo puts a tab at an index inside its own zone: pinned tabs among the pinned, the others among the others. */
function moveTo(state: TabsState, id: string, to: number): TabsState {
  const from = indexOf(state, id);
  const tab = state.tabs[from];
  if (!tab) {
    return state;
  }
  const split = firstUnpinned(state.tabs);
  const lo = tab.pinned ? 0 : split;
  const hi = tab.pinned ? split - 1 : state.tabs.length - 1;
  const target = Math.min(Math.max(to, lo), hi);
  if (target === from) {
    return state;
  }
  const tabs = [...state.tabs];
  tabs.splice(from, 1);
  tabs.splice(target, 0, tab);
  return { ...state, tabs };
}

function update(state: TabsState, id: string, change: (t: Tab) => Tab): TabsState {
  const i = indexOf(state, id);
  const tab = state.tabs[i];
  if (!tab) {
    return state;
  }
  const next = change(tab);
  if (next === tab) {
    return state;
  }
  const tabs = [...state.tabs];
  tabs[i] = next;
  return { ...state, tabs };
}

function openNamed(state: TabsState, id: string, kind: "collection" | "key", name: string): TabsState {
  const existing = state.tabs.find((t) => t.kind === kind && t.table === name);
  if (existing) {
    return { ...state, active: existing.id };
  }
  if (!canOpen(state) || indexOf(state, id) >= 0) {
    return state;
  }
  const tab: Tab = { id, kind, title: name, pinned: false, text: "", savedText: "", table: name };
  return insert(state, tab, state.tabs.length);
}

export function tabsReducer(state: TabsState, action: TabsAction): TabsState {
  switch (action.type) {
    case "openQuery": {
      if (!canOpen(state) || indexOf(state, action.id) >= 0) {
        return state;
      }
      const seq = state.seq + 1;
      const text = action.text ?? "";
      const tab: Tab = { id: action.id, kind: "query", title: `Query ${seq}`, pinned: false, text, savedText: action.text === undefined ? "" : text };
      return insert({ ...state, seq }, tab, state.tabs.length);
    }
    case "openTable": {
      const existing = state.tabs.find((t) => t.kind === "table" && t.table === action.table);
      if (existing) {
        return { ...state, active: existing.id };
      }
      if (!canOpen(state) || indexOf(state, action.id) >= 0) {
        return state;
      }
      const tab: Tab = { id: action.id, kind: "table", title: action.table, pinned: false, text: "", savedText: "", table: action.table };
      return insert(state, tab, state.tabs.length);
    }
    case "openCollection":
      return openNamed(state, action.id, "collection", action.collection);
    case "openKey":
      return openNamed(state, action.id, "key", action.key);
    case "openSchema": {
      const existing = state.tabs.find((t) => t.kind === "schema");
      if (existing) {
        return { ...state, active: existing.id };
      }
      if (!canOpen(state) || indexOf(state, action.id) >= 0) {
        return state;
      }
      const tab: Tab = { id: action.id, kind: "schema", title: "Schema", pinned: false, text: "", savedText: "" };
      return insert(state, tab, state.tabs.length);
    }
    case "close":
      return closeAt(state, indexOf(state, action.id));
    case "closeOthers": {
      if (indexOf(state, action.id) < 0) {
        return state;
      }
      const tabs = state.tabs.filter((t) => t.pinned || t.id === action.id);
      return { ...state, tabs, active: tabs.some((t) => t.id === state.active) ? state.active : action.id };
    }
    case "activate":
      return indexOf(state, action.id) >= 0 ? { ...state, active: action.id } : state;
    case "activateIndex": {
      const tab = state.tabs[action.index];
      return tab ? { ...state, active: tab.id } : state;
    }
    case "step": {
      const n = state.tabs.length;
      if (n === 0) {
        return state;
      }
      const at = Math.max(state.tabs.findIndex((t) => t.id === state.active), 0);
      return { ...state, active: (state.tabs[(at + action.delta + n) % n] as Tab).id };
    }
    case "move":
      return moveTo(state, action.id, action.to);
    case "shift":
      return moveTo(state, action.id, indexOf(state, action.id) + action.delta);
    case "pin": {
      const from = indexOf(state, action.id);
      const tab = state.tabs[from];
      if (!tab || tab.pinned === action.pinned) {
        return state;
      }
      const tabs = state.tabs.filter((_, i) => i !== from);
      const changed = { ...tab, pinned: action.pinned };
      // Pinned: to the end of the pinned tabs. Unpinned: to the start of the others.
      tabs.splice(firstUnpinned(tabs), 0, changed);
      return { ...state, tabs };
    }
    case "duplicate": {
      const from = indexOf(state, action.id);
      const tab = state.tabs[from];
      if (!tab || !canOpen(state) || indexOf(state, action.newId) >= 0) {
        return state;
      }
      if (tab.kind !== "query") {
        return state;
      }
      const seq = state.seq + 1;
      const copy: Tab = { ...tab, id: action.newId, title: `Query ${seq}`, pinned: false, savedText: "" };
      return insert({ ...state, seq }, copy, Math.max(from + 1, firstUnpinned(state.tabs)));
    }
    case "setText":
      return update(state, action.id, (t) => (t.kind === "query" && t.text !== action.text ? { ...t, text: action.text } : t));
    case "markSaved":
      return update(state, action.id, (t) => (t.savedText === t.text ? t : { ...t, savedText: t.text }));
    case "rename":
      return update(state, action.id, (t) => (t.title === action.title || action.title.trim() === "" ? t : { ...t, title: action.title.trim() }));
    case "restore":
      return action.state;
  }
}
