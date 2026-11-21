import { describe, expect, test } from "bun:test";
import { canOpen, emptyTabs, isUnsaved, maxTabs, tabsReducer } from "./tabsState";
import type { TabsAction, TabsState } from "./tabsState";

function run(state: TabsState, ...actions: TabsAction[]): TabsState {
  return actions.reduce(tabsReducer, state);
}

const ids = (s: TabsState): string[] => s.tabs.map((t) => t.id);

function three(): TabsState {
  return run(
    emptyTabs,
    { type: "openQuery", id: "a" },
    { type: "openQuery", id: "b" },
    { type: "openQuery", id: "c" },
  );
}

describe("open", () => {
  test("a new query tab is appended, active and titled by count", () => {
    const s = three();
    expect(ids(s)).toEqual(["a", "b", "c"]);
    expect(s.active).toBe("c");
    expect(s.tabs.map((t) => t.title)).toEqual(["Query 1", "Query 2", "Query 3"]);
  });

  test("titles keep counting after a tab closes", () => {
    const s = run(three(), { type: "close", id: "b" }, { type: "openQuery", id: "d" });
    expect(s.tabs.map((t) => t.title)).toEqual(["Query 1", "Query 3", "Query 4"]);
  });

  test("a table opens once and is activated the second time", () => {
    const s = run(emptyTabs, { type: "openTable", id: "t1", table: "users" }, { type: "openQuery", id: "q" }, { type: "openTable", id: "t2", table: "users" });
    expect(ids(s)).toEqual(["t1", "q"]);
    expect(s.active).toBe("t1");
    expect([s.tabs[0]?.kind, s.tabs[0]?.title, s.tabs[0]?.table]).toEqual(["table", "users", "users"]);
  });

  test("text given at open counts as saved, an empty tab does not need saving", () => {
    const s = run(emptyTabs, { type: "openQuery", id: "a", text: "select 1" }, { type: "openQuery", id: "b" });
    expect(s.tabs.map(isUnsaved)).toEqual([false, false]);
  });

  test("there are at most 20 tabs", () => {
    let s = emptyTabs;
    for (let i = 0; i < maxTabs + 5; i++) {
      s = tabsReducer(s, { type: "openQuery", id: `q${i}` });
    }
    expect(s.tabs.length).toBe(maxTabs);
    expect(canOpen(s)).toBe(false);
    expect(tabsReducer(s, { type: "openTable", id: "x", table: "users" })).toBe(s);
    expect(tabsReducer(s, { type: "duplicate", id: "q0", newId: "z" })).toBe(s);
  });

  test("an id already in use opens nothing", () => {
    const s = three();
    expect(tabsReducer(s, { type: "openQuery", id: "a" })).toBe(s);
  });
});

describe("close", () => {
  test("closing the active tab activates its right neighbour, else the left one", () => {
    expect(run(three(), { type: "activate", id: "b" }, { type: "close", id: "b" }).active).toBe("c");
    expect(run(three(), { type: "close", id: "c" }).active).toBe("b");
    expect(run(three(), { type: "close", id: "a" }).active).toBe("c");
  });

  test("closing the last tab leaves none active", () => {
    const s = run(emptyTabs, { type: "openQuery", id: "a" }, { type: "close", id: "a" });
    expect(s.tabs).toEqual([]);
    expect(s.active).toBeNull();
  });

  test("closing a tab that is not active keeps the active one", () => {
    expect(run(three(), { type: "close", id: "a" }).active).toBe("c");
  });

  test("close others keeps the pinned tabs and the chosen one", () => {
    const s = run(three(), { type: "pin", id: "a", pinned: true }, { type: "closeOthers", id: "c" });
    expect(ids(s)).toEqual(["a", "c"]);
    expect(s.active).toBe("c");
  });

  test("an unknown id changes nothing", () => {
    const s = three();
    expect(tabsReducer(s, { type: "close", id: "zzz" })).toEqual(s);
    expect(tabsReducer(s, { type: "closeOthers", id: "zzz" })).toBe(s);
  });
});

describe("activate", () => {
  test("by id, by index and by stepping around the ends", () => {
    expect(run(three(), { type: "activate", id: "a" }).active).toBe("a");
    expect(run(three(), { type: "activateIndex", index: 1 }).active).toBe("b");
    expect(run(three(), { type: "activateIndex", index: 9 }).active).toBe("c");
    expect(run(three(), { type: "step", delta: 1 }).active).toBe("a");
    expect(run(three(), { type: "step", delta: -1 }).active).toBe("b");
    expect(run(emptyTabs, { type: "step", delta: 1 })).toBe(emptyTabs);
  });
});

describe("reorder", () => {
  test("move puts a tab at an index and shift moves it one step", () => {
    expect(ids(run(three(), { type: "move", id: "a", to: 2 }))).toEqual(["b", "c", "a"]);
    expect(ids(run(three(), { type: "shift", id: "c", delta: -1 }))).toEqual(["a", "c", "b"]);
    expect(ids(run(three(), { type: "shift", id: "a", delta: -1 }))).toEqual(["a", "b", "c"]);
    expect(ids(run(three(), { type: "shift", id: "c", delta: 1 }))).toEqual(["a", "b", "c"]);
  });

  test("a tab keeps its place in the active selection", () => {
    expect(run(three(), { type: "move", id: "a", to: 2 }).active).toBe("c");
  });

  test("pinned tabs stay first and cannot be dragged past the others", () => {
    const s = run(three(), { type: "pin", id: "c", pinned: true });
    expect(ids(s)).toEqual(["c", "a", "b"]);
    expect(ids(run(s, { type: "move", id: "c", to: 2 }))).toEqual(["c", "a", "b"]);
    expect(ids(run(s, { type: "move", id: "b", to: 0 }))).toEqual(["c", "b", "a"]);
  });
});

describe("pin", () => {
  test("pinned tabs go to the front in the order they were pinned, unpinning sends one back", () => {
    let s = run(three(), { type: "pin", id: "b", pinned: true }, { type: "pin", id: "c", pinned: true });
    expect(ids(s)).toEqual(["b", "c", "a"]);
    s = run(s, { type: "pin", id: "b", pinned: false });
    expect(ids(s)).toEqual(["c", "b", "a"]);
    expect(s.tabs.map((t) => t.pinned)).toEqual([true, false, false]);
  });

  test("a new tab opens after the pinned ones", () => {
    const s = run(three(), { type: "pin", id: "a", pinned: true }, { type: "openQuery", id: "d" });
    expect(ids(s)).toEqual(["a", "b", "c", "d"]);
  });
});

describe("duplicate", () => {
  test("copies the text, is unsaved and sits next to the original", () => {
    const s = run(three(), { type: "setText", id: "a", text: "select 1" }, { type: "duplicate", id: "a", newId: "a2" });
    expect(ids(s)).toEqual(["a", "a2", "b", "c"]);
    expect([s.tabs[1]?.text, s.tabs[1]?.savedText, s.tabs[1]?.pinned]).toEqual(["select 1", "", false]);
    expect(s.active).toBe("a2");
  });

  test("a table tab is not duplicated", () => {
    const s = run(emptyTabs, { type: "openTable", id: "t", table: "users" });
    expect(tabsReducer(s, { type: "duplicate", id: "t", newId: "t2" })).toBe(s);
  });
});

describe("text", () => {
  test("setText marks a query unsaved and markSaved clears it", () => {
    let s = run(three(), { type: "setText", id: "a", text: "select 1" });
    expect(isUnsaved(s.tabs[0]!)).toBe(true);
    s = run(s, { type: "markSaved", id: "a" });
    expect(isUnsaved(s.tabs[0]!)).toBe(false);
    s = run(s, { type: "setText", id: "a", text: "select 2" });
    expect(isUnsaved(s.tabs[0]!)).toBe(true);
  });

  test("a table tab has no text to set", () => {
    const s = run(emptyTabs, { type: "openTable", id: "t", table: "users" });
    expect(tabsReducer(s, { type: "setText", id: "t", text: "x" })).toBe(s);
    expect(isUnsaved(s.tabs[0]!)).toBe(false);
  });

  test("setting the same text returns the same state", () => {
    const s = three();
    expect(tabsReducer(s, { type: "setText", id: "a", text: "" })).toBe(s);
  });

  test("rename trims and ignores a blank title", () => {
    expect(run(three(), { type: "rename", id: "a", title: "  Orders  " }).tabs[0]?.title).toBe("Orders");
    expect(run(three(), { type: "rename", id: "a", title: "   " }).tabs[0]?.title).toBe("Query 1");
  });
});
