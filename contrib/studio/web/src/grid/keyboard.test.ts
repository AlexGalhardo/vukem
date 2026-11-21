import { describe, expect, test } from "bun:test";
import { keyAction, selectRange, shouldAnnounce, step } from "./keyboard";
import type { Dims, KeyInput, Pos } from "./keyboard";

const dims: Dims = { rows: 1000, cols: 6, page: 10 };

/** "Ctrl+Shift+ArrowDown" becomes a KeyInput. */
function parse(spec: string): KeyInput {
  const parts = spec.split("+");
  // A plain "+" key would split to empty parts; no sequence below uses it.
  const key = parts[parts.length - 1] ?? "";
  const mods = parts.slice(0, -1).map((m) => m.toLowerCase());
  return {
    key: key === "Space" ? " " : key,
    ctrl: mods.includes("ctrl"),
    meta: mods.includes("meta"),
    shift: mods.includes("shift"),
    alt: mods.includes("alt"),
  };
}

function run(start: Pos, keys: string[]): Pos {
  return keys.reduce((pos, k) => step(pos, dims, parse(k)), start);
}

const sequences: { name: string; start: Pos; keys: string[]; want: Pos }[] = [
  { name: "down", start: { row: 0, col: 1 }, keys: ["ArrowDown"], want: { row: 1, col: 1 } },
  { name: "down three", start: { row: 0, col: 1 }, keys: ["ArrowDown", "ArrowDown", "ArrowDown"], want: { row: 3, col: 1 } },
  { name: "up", start: { row: 5, col: 2 }, keys: ["ArrowUp"], want: { row: 4, col: 2 } },
  { name: "up from the first row reaches the header", start: { row: 0, col: 2 }, keys: ["ArrowUp"], want: { row: -1, col: 2 } },
  { name: "up stops at the header", start: { row: 0, col: 2 }, keys: ["ArrowUp", "ArrowUp", "ArrowUp"], want: { row: -1, col: 2 } },
  { name: "down from the header", start: { row: -1, col: 3 }, keys: ["ArrowDown"], want: { row: 0, col: 3 } },
  { name: "right", start: { row: 2, col: 0 }, keys: ["ArrowRight"], want: { row: 2, col: 1 } },
  { name: "right stops at the last column", start: { row: 2, col: 4 }, keys: ["ArrowRight", "ArrowRight"], want: { row: 2, col: 5 } },
  { name: "left", start: { row: 2, col: 3 }, keys: ["ArrowLeft"], want: { row: 2, col: 2 } },
  { name: "left stops at the gutter", start: { row: 2, col: 1 }, keys: ["ArrowLeft", "ArrowLeft"], want: { row: 2, col: 0 } },
  { name: "down stops at the last row", start: { row: 998, col: 1 }, keys: ["ArrowDown", "ArrowDown", "ArrowDown"], want: { row: 999, col: 1 } },
  { name: "home goes to the row start", start: { row: 7, col: 4 }, keys: ["Home"], want: { row: 7, col: 0 } },
  { name: "end goes to the row end", start: { row: 7, col: 1 }, keys: ["End"], want: { row: 7, col: 5 } },
  { name: "home in the header", start: { row: -1, col: 4 }, keys: ["Home"], want: { row: -1, col: 0 } },
  { name: "ctrl+home goes to the first cell", start: { row: 500, col: 4 }, keys: ["Ctrl+Home"], want: { row: 0, col: 0 } },
  { name: "ctrl+end goes to the last cell", start: { row: 3, col: 1 }, keys: ["Ctrl+End"], want: { row: 999, col: 5 } },
  { name: "meta+end is the same on a Mac", start: { row: 3, col: 1 }, keys: ["Meta+End"], want: { row: 999, col: 5 } },
  { name: "page down", start: { row: 0, col: 2 }, keys: ["PageDown"], want: { row: 10, col: 2 } },
  { name: "page down twice", start: { row: 0, col: 2 }, keys: ["PageDown", "PageDown"], want: { row: 20, col: 2 } },
  { name: "page down from the header", start: { row: -1, col: 2 }, keys: ["PageDown"], want: { row: 10, col: 2 } },
  { name: "page down stops at the end", start: { row: 995, col: 2 }, keys: ["PageDown"], want: { row: 999, col: 2 } },
  { name: "page up", start: { row: 25, col: 2 }, keys: ["PageUp"], want: { row: 15, col: 2 } },
  { name: "page up stops at the first row", start: { row: 4, col: 2 }, keys: ["PageUp", "PageUp"], want: { row: 0, col: 2 } },
  { name: "ctrl+down goes to the last row", start: { row: 4, col: 3 }, keys: ["Ctrl+ArrowDown"], want: { row: 999, col: 3 } },
  { name: "ctrl+up goes to the header", start: { row: 40, col: 3 }, keys: ["Ctrl+ArrowUp"], want: { row: -1, col: 3 } },
  { name: "ctrl+right goes to the last column", start: { row: 4, col: 1 }, keys: ["Ctrl+ArrowRight"], want: { row: 4, col: 5 } },
  { name: "ctrl+left goes to the gutter", start: { row: 4, col: 3 }, keys: ["Ctrl+ArrowLeft"], want: { row: 4, col: 0 } },
  { name: "shift+down moves like down", start: { row: 3, col: 1 }, keys: ["Shift+ArrowDown", "Shift+ArrowDown"], want: { row: 5, col: 1 } },
  { name: "shift+up moves like up", start: { row: 3, col: 1 }, keys: ["Shift+ArrowUp"], want: { row: 2, col: 1 } },
  { name: "a walk around the grid", start: { row: 0, col: 0 }, keys: ["ArrowRight", "ArrowRight", "ArrowDown", "ArrowDown", "ArrowLeft", "ArrowUp"], want: { row: 1, col: 1 } },
  { name: "space does not move", start: { row: 3, col: 2 }, keys: ["Space"], want: { row: 3, col: 2 } },
  { name: "enter does not move", start: { row: 3, col: 2 }, keys: ["Enter"], want: { row: 3, col: 2 } },
  { name: "escape does not move", start: { row: 3, col: 2 }, keys: ["Escape"], want: { row: 3, col: 2 } },
  { name: "tab is left to the browser", start: { row: 3, col: 2 }, keys: ["Tab"], want: { row: 3, col: 2 } },
  { name: "a letter does not move", start: { row: 3, col: 2 }, keys: ["a", "x"], want: { row: 3, col: 2 } },
  { name: "alt+arrow is left to the browser", start: { row: 3, col: 2 }, keys: ["Alt+ArrowLeft"], want: { row: 3, col: 2 } },
  { name: "page then home then down", start: { row: 0, col: 4 }, keys: ["PageDown", "Home", "ArrowDown"], want: { row: 11, col: 0 } },
  { name: "end then up then left", start: { row: 6, col: 0 }, keys: ["End", "ArrowUp", "ArrowLeft"], want: { row: 5, col: 4 } },
  { name: "ctrl+end then ctrl+home", start: { row: 6, col: 3 }, keys: ["Ctrl+End", "Ctrl+Home"], want: { row: 0, col: 0 } },
  { name: "up from the header stays", start: { row: -1, col: 0 }, keys: ["ArrowUp", "ArrowLeft"], want: { row: -1, col: 0 } },
];

describe("keyboard model", () => {
  test("has 40 sequences", () => {
    expect(sequences).toHaveLength(40);
  });

  for (const s of sequences) {
    test(s.name, () => {
      expect(run(s.start, s.keys)).toEqual(s.want);
    });
  }
});

describe("keyAction", () => {
  const at: Pos = { row: 5, col: 2 };

  test("Enter activates and F2 edits", () => {
    expect(keyAction(at, dims, { key: "Enter" })).toEqual({ type: "activate", edit: false });
    expect(keyAction(at, dims, { key: "F2" })).toEqual({ type: "activate", edit: true });
  });

  test("Space toggles the selection, Escape cancels", () => {
    expect(keyAction(at, dims, { key: " " })).toEqual({ type: "toggleSelect" });
    expect(keyAction(at, dims, { key: "Escape" })).toEqual({ type: "cancel" });
  });

  test("Ctrl or Cmd+C copies, a bare c does not", () => {
    expect(keyAction(at, dims, { key: "c", ctrl: true })).toEqual({ type: "copy" });
    expect(keyAction(at, dims, { key: "c", meta: true })).toEqual({ type: "copy" });
    expect(keyAction(at, dims, { key: "c" })).toEqual({ type: "none" });
  });

  test("Shift+Arrow asks to extend the selection, but not from or into the header", () => {
    expect(keyAction(at, dims, { key: "ArrowDown", shift: true })).toEqual({
      type: "move",
      to: { row: 6, col: 2 },
      extend: true,
    });
    expect(keyAction({ row: 0, col: 1 }, dims, { key: "ArrowUp", shift: true })).toEqual({
      type: "move",
      to: { row: -1, col: 1 },
      extend: false,
    });
    expect(keyAction(at, dims, { key: "ArrowDown" })).toEqual({ type: "move", to: { row: 6, col: 2 }, extend: false });
  });

  test("Ctrl+End asks to jump to the end", () => {
    expect(keyAction(at, dims, { key: "End", ctrl: true })).toEqual({ type: "jumpEnd", to: { row: 999, col: 5 } });
  });

  test("the menu keys", () => {
    expect(keyAction(at, dims, { key: "ContextMenu" })).toEqual({ type: "menu" });
    expect(keyAction(at, dims, { key: "F10", shift: true })).toEqual({ type: "menu" });
    expect(keyAction(at, dims, { key: "F10" })).toEqual({ type: "none" });
  });

  test("an empty grid keeps the focus on the header and the first row", () => {
    const empty: Dims = { rows: 0, cols: 3, page: 10 };
    expect(step({ row: -1, col: 0 }, empty, { key: "ArrowDown" })).toEqual({ row: 0, col: 0 });
    expect(step({ row: 0, col: 0 }, empty, { key: "ArrowDown" })).toEqual({ row: 0, col: 0 });
  });
});

describe("selection and announcements", () => {
  test("selectRange is inclusive and order-free", () => {
    expect(selectRange(3, 6)).toEqual([3, 4, 5, 6]);
    expect(selectRange(6, 3)).toEqual([3, 4, 5, 6]);
    expect(selectRange(4, 4)).toEqual([4]);
  });

  test("announcements come in coarse steps", () => {
    expect(shouldAnnounce(10, 11)).toBe(false);
    expect(shouldAnnounce(99, 100)).toBe(true);
    expect(shouldAnnounce(100, 99)).toBe(true);
    expect(shouldAnnounce(5, 500)).toBe(true);
    expect(shouldAnnounce(120, 180)).toBe(false);
  });
});
