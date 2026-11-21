import { describe, expect, test } from "bun:test";
import { loadTabs, maxTextBytes, parseTabs, saveTabs, serializeTabs, tabsKey } from "./persist";
import type { StorageLike } from "./persist";
import { emptyTabs, maxTabs, tabsReducer } from "./tabsState";
import type { TabsAction, TabsState } from "./tabsState";

function memory(): StorageLike & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

function build(...actions: TabsAction[]): TabsState {
  return actions.reduce(tabsReducer, emptyTabs);
}

describe("persistence", () => {
  test("tabs and the text of each query come back after a reload", () => {
    const state = build(
      { type: "openQuery", id: "a" },
      { type: "setText", id: "a", text: "select 1" },
      { type: "openTable", id: "t", table: "users" },
      { type: "openQuery", id: "b" },
      { type: "setText", id: "b", text: "select 2" },
      { type: "markSaved", id: "b" },
      { type: "pin", id: "t", pinned: true },
      { type: "activate", id: "a" },
    );
    const storage = memory();
    saveTabs(storage, "db", state);
    expect(loadTabs(storage, "db")).toEqual(state);
  });

  test("each source has its own key", () => {
    const storage = memory();
    saveTabs(storage, "one", build({ type: "openQuery", id: "a" }));
    expect(loadTabs(storage, "two")).toBeNull();
    expect(tabsKey("one")).not.toBe(tabsKey("two"));
  });

  test("no result data is persisted: only the fields of a tab are written", () => {
    // A state that has picked up extra fields (a result, a row) must not carry them to storage.
    const state = build({ type: "openQuery", id: "a" }, { type: "setText", id: "a", text: "select 1" });
    const polluted = {
      ...state,
      tabs: state.tabs.map((t) => ({ ...t, result: { rows: [["secret-value"]] }, rows: [["secret-value"]], writeMode: true })),
    } as unknown as TabsState;
    const raw = serializeTabs(polluted);
    expect(raw).not.toContain("secret-value");
    expect(raw).not.toContain("writeMode");
    expect(raw).not.toContain("result");
    expect(Object.keys(JSON.parse(raw).tabs[0]).sort()).toEqual(["id", "kind", "pinned", "savedText", "text", "title"]);
  });

  test("a table tab stores no text", () => {
    const raw = serializeTabs(build({ type: "openTable", id: "t", table: "users" }));
    expect(JSON.parse(raw).tabs[0]).toEqual({ id: "t", kind: "table", title: "users", pinned: false, text: "", savedText: "", table: "users" });
  });

  test("corrupt, foreign or empty values are ignored", () => {
    for (const raw of [null, "", "{", "[]", "null", '{"v":2,"tabs":[]}', '{"v":1,"tabs":[]}', '{"v":1,"tabs":"x"}', '{"v":1,"tabs":[{"id":1}]}']) {
      expect(parseTabs(raw)).toBeNull();
    }
  });

  test("bad tabs are dropped and good ones kept", () => {
    const raw = JSON.stringify({
      v: 1,
      active: "missing",
      seq: 5,
      tabs: [
        { id: "a", kind: "query", title: "Q", pinned: false, text: "select 1", savedText: "" },
        { id: "a", kind: "query", title: "dup", pinned: false, text: "", savedText: "" },
        { id: "b", kind: "weird", title: "x", pinned: false, text: "", savedText: "" },
        { id: "c", kind: "table", title: "t", pinned: false },
        { id: "d", kind: "query", title: "big", pinned: false, text: "x".repeat(maxTextBytes + 1), savedText: "" },
      ],
    });
    const state = parseTabs(raw);
    expect(state?.tabs.map((t) => t.id)).toEqual(["a"]);
    expect(state?.active).toBe("a");
    expect(state?.seq).toBe(5);
  });

  test("no more than 20 tabs are restored, pinned first", () => {
    const tabs = Array.from({ length: 30 }, (_, i) => ({ id: `q${i}`, kind: "query", title: `Q${i}`, pinned: i === 25, text: "", savedText: "" }));
    const state = parseTabs(JSON.stringify({ v: 1, active: "q0", seq: 30, tabs }));
    expect(state?.tabs.length).toBe(maxTabs);
    expect(state?.tabs[0]?.id).toBe("q0");
  });

  test("storage that throws does not break the page", () => {
    const broken: StorageLike = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("full");
      },
      removeItem: () => undefined,
    };
    expect(loadTabs(broken, "db")).toBeNull();
    expect(() => saveTabs(broken, "db", build({ type: "openQuery", id: "a" }))).not.toThrow();
  });
});
