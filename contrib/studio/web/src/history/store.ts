import type { RunState } from "../console/runner";

/**
 * Query history and the saved-query library, both in the browser's
 * localStorage and nowhere else. Statements can hold sensitive literals, so:
 * history lives under a key that names the Studio run (and the keys of earlier
 * runs are deleted at startup), it can be cleared or switched off, and it never
 * holds a result: only the statement and a few numbers about the run. The
 * library is the person's own named list and stays across runs.
 */

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  readonly length: number;
  key(index: number): string | null;
}

export const maxHistory = 200;
export const maxSaved = 100;
/** A longer statement is not stored: cutting it would make "run again" run something else. */
export const maxStatementChars = 16 << 10;

const historyPrefix = "vukem.studio.history.";
export const offKey = "vukem.studio.historyOff";
export const libraryKey = "vukem.studio.library";

export function historyKey(sessionKey: string): string {
  return historyPrefix + sessionKey;
}

export type Outcome = "ok" | "cancelled" | "input" | "timeout" | "database" | "refused" | "network" | "other";

export interface HistoryEntry {
  id: string;
  sql: string;
  source: string;
  /** When the run ended, in milliseconds since the epoch. */
  at: number;
  durationMs: number;
  /** Rows returned or affected; null when the run failed. */
  rows: number | null;
  outcome: Outcome;
  /** The statement's verb. */
  kind: string;
}

export interface SavedQuery {
  id: string;
  name: string;
  sql: string;
  source: string;
  savedAt: number;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

const outcomes: readonly string[] = ["ok", "cancelled", "input", "timeout", "database", "refused", "network", "other"];

function readEntry(v: unknown): HistoryEntry | null {
  if (!isRecord(v)) {
    return null;
  }
  const { id, sql, source, at, durationMs, rows, outcome, kind } = v;
  if (
    typeof id !== "string" ||
    typeof sql !== "string" ||
    sql.length > maxStatementChars ||
    typeof source !== "string" ||
    typeof at !== "number" ||
    typeof durationMs !== "number" ||
    (rows !== null && typeof rows !== "number") ||
    typeof outcome !== "string" ||
    !outcomes.includes(outcome) ||
    typeof kind !== "string"
  ) {
    return null;
  }
  return { id, sql, source, at, durationMs, rows, outcome: outcome as Outcome, kind };
}

function readSaved(v: unknown): SavedQuery | null {
  if (!isRecord(v)) {
    return null;
  }
  const { id, name, sql, source, savedAt } = v;
  if (typeof id !== "string" || typeof name !== "string" || typeof sql !== "string" || typeof source !== "string" || typeof savedAt !== "number") {
    return null;
  }
  return { id, name, sql, source, savedAt };
}

function readList<T>(storage: StorageLike, key: string, read: (v: unknown) => T | null, cap: number): T[] {
  try {
    const raw = storage.getItem(key);
    if (raw === null) {
      return [];
    }
    const data: unknown = JSON.parse(raw);
    if (!Array.isArray(data)) {
      return [];
    }
    return data.map(read).filter((e): e is T => e !== null).slice(-cap);
  } catch {
    return [];
  }
}

/** write stores a list; when the browser says the quota is full it drops the oldest half and tries once more. */
function writeList<T>(storage: StorageLike, key: string, list: T[]): T[] {
  let current = list;
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      storage.setItem(key, JSON.stringify(current));
      return current;
    } catch {
      current = current.slice(Math.ceil(current.length / 2));
    }
  }
  return current;
}

/**
 * purgeOldHistory deletes the history of earlier Studio runs: their statements
 * can hold literals nobody wants to keep around. The library and the setting stay.
 */
export function purgeOldHistory(storage: StorageLike, sessionKey: string): void {
  const keep = historyKey(sessionKey);
  const doomed: string[] = [];
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key !== null && key.startsWith(historyPrefix) && key !== keep) {
      doomed.push(key);
    }
  }
  doomed.forEach((key) => storage.removeItem(key));
}

/** entryFrom describes a finished run for the history: the statement and numbers, never the rows. */
export function entryFrom(id: string, source: string, state: RunState, at: number): HistoryEntry | null {
  switch (state.status) {
    case "done": {
      const rows = state.result.affected ?? state.result.rows.length;
      return { id, sql: state.statement, source, at, durationMs: state.result.elapsedMs, rows, outcome: "ok", kind: state.result.kind };
    }
    case "error":
      return { id, sql: state.statement, source, at, durationMs: 0, rows: null, outcome: state.error.kind, kind: "" };
    case "cancelled":
      return { id, sql: state.statement, source, at, durationMs: 0, rows: null, outcome: "cancelled", kind: "" };
    default:
      return null;
  }
}

/** matches is the history search: a case-insensitive substring of the statement. */
export function matches(entry: { sql: string }, query: string): boolean {
  return query.trim() === "" || entry.sql.toLowerCase().includes(query.trim().toLowerCase());
}

export interface Snapshot {
  history: readonly HistoryEntry[];
  saved: readonly SavedQuery[];
  enabled: boolean;
}

/** HistoryStore holds the history and the library of this browser and tells the interface when they change. */
export class HistoryStore {
  private history: HistoryEntry[];
  private saved: SavedQuery[];
  private enabled: boolean;
  private snap: Snapshot;
  private readonly listeners = new Set<() => void>();

  constructor(
    private readonly storage: StorageLike,
    private readonly sessionKey: string,
  ) {
    this.enabled = this.readEnabled();
    this.history = this.enabled ? readList(storage, historyKey(sessionKey), readEntry, maxHistory) : [];
    this.saved = readList(storage, libraryKey, readSaved, maxSaved);
    this.snap = this.build();
  }

  private readEnabled(): boolean {
    try {
      return this.storage.getItem(offKey) !== "1";
    } catch {
      return true;
    }
  }

  private build(): Snapshot {
    return { history: this.history, saved: this.saved, enabled: this.enabled };
  }

  private changed(): void {
    this.snap = this.build();
    this.listeners.forEach((l) => l());
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): Snapshot => this.snap;

  private saveHistory(): void {
    this.history = writeList(this.storage, historyKey(this.sessionKey), this.history);
  }

  /** add records a run. It does nothing while history is off, or for a statement too long to keep whole. */
  add(entry: HistoryEntry): void {
    if (!this.enabled || entry.sql.length > maxStatementChars || entry.sql.trim() === "") {
      return;
    }
    this.history = [...this.history, entry].slice(-maxHistory);
    this.saveHistory();
    this.changed();
  }

  remove(id: string): void {
    this.history = this.history.filter((e) => e.id !== id);
    this.saveHistory();
    this.changed();
  }

  clear(): void {
    this.history = [];
    try {
      this.storage.removeItem(historyKey(this.sessionKey));
    } catch {
      // Nothing was stored that could be removed.
    }
    this.changed();
  }

  /** setEnabled turns history on or off. Turning it off also deletes what was kept: "off" means nothing stays. */
  setEnabled(on: boolean): void {
    this.enabled = on;
    try {
      if (on) {
        this.storage.removeItem(offKey);
      } else {
        this.storage.setItem(offKey, "1");
      }
    } catch {
      // The choice then lasts until reload.
    }
    if (!on) {
      this.clear();
      return;
    }
    this.changed();
  }

  private saveLibrary(): void {
    this.saved = writeList(this.storage, libraryKey, this.saved);
  }

  /** save stores a query under a name for its source; the same name replaces the text. It refuses when the library is full. */
  save(name: string, sql: string, source: string, id: string, now: number): "saved" | "replaced" | "full" | "invalid" {
    const trimmed = name.trim();
    if (trimmed === "" || sql.trim() === "" || sql.length > maxStatementChars) {
      return "invalid";
    }
    const at = this.saved.findIndex((q) => q.source === source && q.name === trimmed);
    if (at >= 0) {
      this.saved = this.saved.map((q, i) => (i === at ? { ...q, sql, savedAt: now } : q));
      this.saveLibrary();
      this.changed();
      return "replaced";
    }
    if (this.saved.length >= maxSaved) {
      return "full";
    }
    this.saved = [...this.saved, { id, name: trimmed, sql, source, savedAt: now }];
    this.saveLibrary();
    this.changed();
    return "saved";
  }

  /** rename gives a saved query a new name; a name already used for that source is refused. */
  rename(id: string, name: string): boolean {
    const trimmed = name.trim();
    const target = this.saved.find((q) => q.id === id);
    if (!target || trimmed === "" || this.saved.some((q) => q.id !== id && q.source === target.source && q.name === trimmed)) {
      return false;
    }
    this.saved = this.saved.map((q) => (q.id === id ? { ...q, name: trimmed } : q));
    this.saveLibrary();
    this.changed();
    return true;
  }

  deleteSaved(id: string): void {
    this.saved = this.saved.filter((q) => q.id !== id);
    this.saveLibrary();
    this.changed();
  }
}

const stores = new Map<string, HistoryStore>();

/** openHistory gives the one store of a Studio run in this page. */
export function openHistory(storage: StorageLike, sessionKey: string): HistoryStore {
  let store = stores.get(sessionKey);
  if (!store) {
    store = new HistoryStore(storage, sessionKey);
    stores.set(sessionKey, store);
  }
  return store;
}
