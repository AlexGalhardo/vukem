import { describe, expect, test } from "bun:test";
import type { QueryResponse } from "../api/types";
import type { RunState } from "../console/runner";
import {
  entryFrom,
  HistoryStore,
  historyKey,
  libraryKey,
  matches,
  maxHistory,
  maxSaved,
  maxStatementChars,
  offKey,
  purgeOldHistory,
} from "./store";
import type { HistoryEntry, StorageLike } from "./store";

class Memory implements StorageLike {
  data = new Map<string, string>();
  failNext = 0;

  get length(): number {
    return this.data.size;
  }
  key(i: number): string | null {
    return [...this.data.keys()][i] ?? null;
  }
  getItem(k: string): string | null {
    return this.data.get(k) ?? null;
  }
  setItem(k: string, v: string): void {
    if (this.failNext > 0) {
      this.failNext--;
      throw new Error("QuotaExceededError");
    }
    this.data.set(k, v);
  }
  removeItem(k: string): void {
    this.data.delete(k);
  }
}

function entry(n: number, over: Partial<HistoryEntry> = {}): HistoryEntry {
  return { id: `e${n}`, sql: `select ${n}`, source: "db", at: n, durationMs: 1, rows: 1, outcome: "ok", kind: "SELECT", ...over };
}

describe("history", () => {
  test("keeps at most 200 entries, the newest", () => {
    const store = new HistoryStore(new Memory(), "s1");
    for (let i = 0; i < maxHistory + 25; i++) {
      store.add(entry(i));
    }
    const list = store.getSnapshot().history;
    expect(list.length).toBe(maxHistory);
    expect(list[0]?.sql).toBe("select 25");
    expect(list[list.length - 1]?.sql).toBe(`select ${maxHistory + 24}`);
  });

  test("is stored under a key that names the run, and read back by the same run", () => {
    const storage = new Memory();
    new HistoryStore(storage, "abc").add(entry(1));
    expect([...storage.data.keys()]).toEqual([historyKey("abc")]);
    expect(new HistoryStore(storage, "abc").getSnapshot().history.length).toBe(1);
    expect(new HistoryStore(storage, "other").getSnapshot().history.length).toBe(0);
  });

  test("a new Studio run deletes the history of earlier runs and keeps the library and the setting", () => {
    const storage = new Memory();
    new HistoryStore(storage, "old1").add(entry(1));
    new HistoryStore(storage, "old2").add(entry(2));
    const current = new HistoryStore(storage, "now");
    current.add(entry(3));
    current.save("mine", "select 9", "db", "q1", 1);
    current.setEnabled(true);
    storage.setItem(offKey, "1");
    storage.setItem("something.else", "x");

    purgeOldHistory(storage, "now");
    expect([...storage.data.keys()].sort()).toEqual([historyKey("now"), libraryKey, offKey, "something.else"].sort());
  });

  test("search is a case-insensitive substring of the statement", () => {
    expect(matches(entry(1, { sql: "SELECT * FROM Users" }), "users")).toBe(true);
    expect(matches(entry(1, { sql: "select 1" }), "users")).toBe(false);
    expect(matches(entry(1), "  ")).toBe(true);
  });

  test("remove and clear", () => {
    const storage = new Memory();
    const store = new HistoryStore(storage, "s");
    store.add(entry(1));
    store.add(entry(2));
    store.remove("e1");
    expect(store.getSnapshot().history.map((e) => e.id)).toEqual(["e2"]);
    store.clear();
    expect(store.getSnapshot().history).toEqual([]);
    expect(storage.getItem(historyKey("s"))).toBeNull();
  });

  test("the setting that disables storage stores nothing and deletes what was kept", () => {
    const storage = new Memory();
    const store = new HistoryStore(storage, "s");
    store.add(entry(1));
    store.setEnabled(false);
    expect(store.getSnapshot().enabled).toBe(false);
    expect(store.getSnapshot().history).toEqual([]);
    store.add(entry(2));
    expect(store.getSnapshot().history).toEqual([]);
    expect(storage.getItem(historyKey("s"))).toBeNull();

    // The choice outlasts the page.
    expect(new HistoryStore(storage, "s2").getSnapshot().enabled).toBe(false);
    store.setEnabled(true);
    store.add(entry(3));
    expect(store.getSnapshot().history.length).toBe(1);
  });

  test("stores no results: an entry holds the statement and numbers only", () => {
    const result: QueryResponse = {
      columns: ["email"],
      rows: [[{ t: "secret@example.test" }], [{ t: "other@example.test" }]],
      truncated: false,
      elapsedMs: 4,
      kind: "SELECT",
    };
    const state: RunState = { status: "done", id: 1, kind: "run", statement: "select email from users", result, roundTripMs: 9 };
    const e = entryFrom("x", "db", state, 100);
    expect(e).toEqual({ id: "x", sql: "select email from users", source: "db", at: 100, durationMs: 4, rows: 2, outcome: "ok", kind: "SELECT" });

    const storage = new Memory();
    new HistoryStore(storage, "s").add(e as HistoryEntry);
    const raw = JSON.stringify([...storage.data.values()]);
    expect(raw).not.toContain("secret@example.test");
    expect(raw).not.toContain("columns");
  });

  test("a write records the rows it changed; a failure records its kind and no rows", () => {
    const write: RunState = {
      status: "done",
      id: 1,
      kind: "run",
      statement: "update t set a = 1",
      result: { columns: [], rows: [], truncated: false, elapsedMs: 2, kind: "UPDATE", affected: 7 },
      roundTripMs: 3,
    };
    expect(entryFrom("w", "db", write, 1)?.rows).toBe(7);
    const failed: RunState = { status: "error", kind: "run", statement: "select nope", error: { kind: "database", label: "Database error", message: "x" } };
    const f = entryFrom("f", "db", failed, 1);
    expect([f?.rows, f?.outcome]).toEqual([null, "database"]);
    expect(entryFrom("c", "db", { status: "cancelled", kind: "run", statement: "select 1" }, 1)?.outcome).toBe("cancelled");
    expect(entryFrom("i", "db", { status: "idle" }, 1)).toBeNull();
    expect(entryFrom("r", "db", { status: "running", kind: "run", statement: "s", startedAt: 0 }, 1)).toBeNull();
  });

  test("a statement too long to keep whole is not stored", () => {
    const store = new HistoryStore(new Memory(), "s");
    store.add(entry(1, { sql: "x".repeat(maxStatementChars + 1) }));
    store.add(entry(2, { sql: "   " }));
    expect(store.getSnapshot().history).toEqual([]);
  });

  test("a full browser quota drops the oldest entries instead of failing", () => {
    const storage = new Memory();
    const store = new HistoryStore(storage, "s");
    for (let i = 0; i < 10; i++) {
      store.add(entry(i));
    }
    storage.failNext = 1;
    store.add(entry(10));
    const kept = store.getSnapshot().history;
    expect(kept.length < 11).toBe(true);
    expect(kept[kept.length - 1]?.id).toBe("e10");
  });

  test("corrupt or foreign stored values are ignored", () => {
    const storage = new Memory();
    storage.setItem(historyKey("s"), '[{"id":1},"x",null,{"id":"ok","sql":"select 1","source":"db","at":1,"durationMs":1,"rows":null,"outcome":"ok","kind":"SELECT"}]');
    expect(new HistoryStore(storage, "s").getSnapshot().history.map((e) => e.id)).toEqual(["ok"]);
    storage.setItem(historyKey("s"), "{");
    expect(new HistoryStore(storage, "s").getSnapshot().history).toEqual([]);
    storage.setItem(historyKey("s"), '{"a":1}');
    expect(new HistoryStore(storage, "s").getSnapshot().history).toEqual([]);
  });

  test("notifies subscribers", () => {
    const store = new HistoryStore(new Memory(), "s");
    let n = 0;
    const off = store.subscribe(() => n++);
    store.add(entry(1));
    const before = store.getSnapshot();
    store.remove("e1");
    off();
    store.add(entry(2));
    expect(n).toBe(2);
    expect(store.getSnapshot()).not.toBe(before);
  });
});

describe("library", () => {
  test("saves, replaces by name, renames and deletes, and survives a new run", () => {
    const storage = new Memory();
    const store = new HistoryStore(storage, "run1");
    expect(store.save("Users", "select * from users", "db", "a", 1)).toBe("saved");
    expect(store.save("Users", "select id from users", "db", "b", 2)).toBe("replaced");
    expect(store.getSnapshot().saved.length).toBe(1);
    expect(store.getSnapshot().saved[0]?.sql).toBe("select id from users");

    expect(store.save("Orders", "select 1", "db", "c", 3)).toBe("saved");
    expect(store.rename("c", "Users")).toBe(false);
    expect(store.rename("c", "  Orders 2 ")).toBe(true);
    expect(store.getSnapshot().saved.map((q) => q.name)).toEqual(["Users", "Orders 2"]);

    purgeOldHistory(storage, "run2");
    expect(new HistoryStore(storage, "run2").getSnapshot().saved.map((q) => q.name)).toEqual(["Users", "Orders 2"]);

    store.deleteSaved("a");
    expect(store.getSnapshot().saved.map((q) => q.name)).toEqual(["Orders 2"]);
  });

  test("the same name in another source is a different query", () => {
    const store = new HistoryStore(new Memory(), "s");
    expect(store.save("Q", "select 1", "one", "a", 1)).toBe("saved");
    expect(store.save("Q", "select 2", "two", "b", 1)).toBe("saved");
    expect(store.getSnapshot().saved.length).toBe(2);
  });

  test("holds at most 100 queries and refuses blank names and texts", () => {
    const store = new HistoryStore(new Memory(), "s");
    for (let i = 0; i < maxSaved; i++) {
      expect(store.save(`q${i}`, "select 1", "db", `id${i}`, i)).toBe("saved");
    }
    expect(store.save("one more", "select 1", "db", "x", 1)).toBe("full");
    expect(store.save("q0", "select 2", "db", "y", 1)).toBe("replaced");
    expect(store.save("  ", "select 1", "db", "z", 1)).toBe("invalid");
    expect(store.save("n", "  ", "db", "z", 1)).toBe("invalid");
    expect(store.getSnapshot().saved.length).toBe(maxSaved);
  });

  test("history off does not touch the library", () => {
    const store = new HistoryStore(new Memory(), "s");
    store.save("Keep", "select 1", "db", "a", 1);
    store.setEnabled(false);
    expect(store.getSnapshot().saved.length).toBe(1);
  });
});
