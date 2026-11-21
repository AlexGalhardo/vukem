import { describe, expect, test } from "bun:test";
import { isTypingTarget, refusal, ShortcutRegistry } from "./registry";
import type { KeyEventLike } from "./registry";

/** A stand-in for a DOM element: it knows its tag and whether it is editable. */
function element(tag: string, options: { editable?: boolean; inside?: string } = {}): unknown {
  const fieldTags = ["input", "textarea", "select"];
  return {
    isContentEditable: options.editable === true,
    closest: (selector: string): unknown => {
      const tags = [tag, ...(options.inside ? [options.inside] : [])];
      const matches = tags.some((t) => fieldTags.includes(t) && selector.includes(t));
      const editable = options.editable === true && selector.includes("contenteditable");
      return matches || editable ? {} : null;
    },
  };
}

function key(k: string, extra: Partial<KeyEventLike> = {}): KeyEventLike {
  return {
    key: k,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: element("div"),
    ...extra,
  };
}

describe("refusal", () => {
  test("browser and system combinations are refused", () => {
    for (const k of [
      "ctrl+r", "ctrl+t", "ctrl+w", "ctrl+n", "ctrl+l", "ctrl+f", "ctrl+p", "ctrl+s", "ctrl+shift+i", "meta+l", "cmd+r",
      "alt+ArrowLeft", "alt+Home", "ctrl+Tab", "F5", "F12", "f1",
    ]) {
      expect(refusal(k) === null).toBe(false);
    }
  });

  test("keys with another meaning are refused", () => {
    for (const k of ["Tab", "Escape", "Enter", " ", "Backspace", "ArrowDown", "Home", "End", "PageDown", "Delete", ""]) {
      expect(refusal(k) === null).toBe(false);
    }
    expect(refusal("ab") === null).toBe(false);
  });

  test("single characters are allowed", () => {
    for (const k of ["/", "r", "n", "?", "g"]) {
      expect(refusal(k)).toBeNull();
    }
  });
});

describe("isTypingTarget", () => {
  test("inputs, textareas, selects and editable elements", () => {
    expect(isTypingTarget(element("input"))).toBe(true);
    expect(isTypingTarget(element("textarea"))).toBe(true);
    expect(isTypingTarget(element("select"))).toBe(true);
    expect(isTypingTarget(element("div", { editable: true }))).toBe(true);
    expect(isTypingTarget(element("span", { inside: "input" }))).toBe(true);
  });

  test("other elements and non-elements are not", () => {
    expect(isTypingTarget(element("div"))).toBe(false);
    expect(isTypingTarget(element("button"))).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget(undefined)).toBe(false);
    expect(isTypingTarget("input")).toBe(false);
  });
});

describe("ShortcutRegistry", () => {
  test("runs the shortcut of a plain key", () => {
    const r = new ShortcutRegistry();
    let ran = 0;
    r.register({ key: "r", description: "refresh", run: () => ran++ });
    expect(r.handle(key("r"))).toBe(true);
    expect(ran).toBe(1);
    expect(r.handle(key("x"))).toBe(false);
  });

  test("fires nothing while typing in an input, textarea, select or editable element", () => {
    const r = new ShortcutRegistry();
    let ran = 0;
    r.register({ key: "/", description: "filter", run: () => ran++ });

    for (const target of [
      element("input"), element("textarea"), element("select"), element("div", { editable: true }),
      element("span", { inside: "textarea" }),
    ]) {
      expect(r.handle(key("/", { target }))).toBe(false);
    }
    expect(ran).toBe(0);
    expect(r.handle(key("/", { target: element("button") }))).toBe(true);
    expect(ran).toBe(1);
  });

  test("ignores modified keys, repeats, composition and handled events", () => {
    const r = new ShortcutRegistry();
    let ran = 0;
    r.register({ key: "r", description: "refresh", run: () => ran++ });

    expect(r.handle(key("r", { ctrlKey: true }))).toBe(false);
    expect(r.handle(key("r", { metaKey: true }))).toBe(false);
    expect(r.handle(key("r", { altKey: true }))).toBe(false);
    expect(r.handle(key("r", { repeat: true }))).toBe(false);
    expect(r.handle(key("r", { isComposing: true }))).toBe(false);
    expect(r.handle(key("r", { defaultPrevented: true }))).toBe(false);
    expect(ran).toBe(0);
  });

  test("a disabled shortcut does nothing and is not listed", () => {
    const r = new ShortcutRegistry();
    let write = false;
    let ran = 0;
    r.register({ key: "n", description: "new row", run: () => ran++, enabled: () => write });
    r.register({ key: "?", description: "help", run: () => undefined });

    expect(r.handle(key("n"))).toBe(false);
    expect(r.list().map((s) => s.key)).toEqual(["?"]);

    write = true;
    expect(r.handle(key("n"))).toBe(true);
    expect(r.list().map((s) => s.key)).toEqual(["n", "?"]);
    expect(ran).toBe(1);
  });

  test("refuses reserved keys and duplicates when registering", () => {
    const r = new ShortcutRegistry();
    expect(() => r.register({ key: "ctrl+r", description: "x", run: () => undefined })).toThrow();
    expect(() => r.register({ key: "F5", description: "x", run: () => undefined })).toThrow();
    r.register({ key: "g", description: "x", run: () => undefined });
    expect(() => r.register({ key: "g", description: "y", run: () => undefined })).toThrow();
  });

  test("the function returned by register removes the shortcut", () => {
    const r = new ShortcutRegistry();
    const off = r.register({ key: "g", description: "x", run: () => undefined });
    off();
    expect(r.list()).toEqual([]);
    expect(r.handle(key("g"))).toBe(false);
    r.register({ key: "g", description: "again", run: () => undefined });
    expect(r.list()).toHaveLength(1);
  });
});

describe("chords", () => {
  function setup(): { r: ShortcutRegistry; ran: string[]; tick: (ms: number) => void } {
    let clock = 0;
    const r = new ShortcutRegistry(() => clock);
    const ran: string[] = [];
    r.register({ key: "q n", description: "New query tab", run: () => ran.push("qn") });
    r.register({ key: "w c", description: "Close tab", run: () => ran.push("wc") });
    r.register({ key: "]", description: "Next tab", run: () => ran.push("next") });
    return { r, ran, tick: (ms) => void (clock += ms) };
  }

  test("two plain keys in a row run the chord, and the first key alone does nothing", () => {
    const { r, ran } = setup();
    expect(r.handle(key("q"))).toBe(true);
    expect(ran).toEqual([]);
    expect(r.handle(key("n"))).toBe(true);
    expect(ran).toEqual(["qn"]);
    // The chord is spent: n alone does nothing.
    expect(r.handle(key("n"))).toBe(false);
    expect(ran).toEqual(["qn"]);
  });

  test("the second key must come in time", () => {
    const { r, ran, tick } = setup();
    r.handle(key("q"));
    tick(2000);
    expect(r.handle(key("n"))).toBe(false);
    expect(ran).toEqual([]);
  });

  test("another key after the first drops the chord and is handled by itself", () => {
    const { r, ran } = setup();
    r.handle(key("q"));
    expect(r.handle(key("]"))).toBe(true);
    expect(ran).toEqual(["next"]);
    expect(r.handle(key("n"))).toBe(false);
  });

  test("typing in a field neither starts nor finishes a chord", () => {
    const { r, ran } = setup();
    expect(r.handle(key("q", { target: element("textarea") }))).toBe(false);
    expect(r.handle(key("n", { target: element("textarea") }))).toBe(false);
    r.handle(key("q"));
    expect(r.handle(key("n", { target: element("input") }))).toBe(false);
    // The field keystroke cancelled the chord: a page-level n after it does not complete it either.
    expect(r.handle(key("n"))).toBe(false);
    expect(ran).toEqual([]);
  });

  test("a modified key cancels a chord", () => {
    const { r, ran } = setup();
    r.handle(key("q"));
    expect(r.handle(key("n", { ctrlKey: true }))).toBe(false);
    expect(r.handle(key("n"))).toBe(false);
    expect(ran).toEqual([]);
  });

  test("a disabled chord is not started and not listed", () => {
    const r = new ShortcutRegistry(() => 0);
    let on = false;
    r.register({ key: "w c", description: "Close", run: () => undefined, enabled: () => on });
    expect(r.handle(key("w"))).toBe(false);
    expect(r.list()).toEqual([]);
    on = true;
    expect(r.handle(key("w"))).toBe(true);
    expect(r.list()).toEqual([{ key: "w c", description: "Close" }]);
  });

  test("chords are checked like single keys, and cannot clash with them", () => {
    const r = new ShortcutRegistry(() => 0);
    expect(() => r.register({ key: "q ctrl+n", description: "x", run: () => undefined })).toThrow();
    expect(() => r.register({ key: "q Tab", description: "x", run: () => undefined })).toThrow();
    r.register({ key: "q n", description: "x", run: () => undefined });
    expect(() => r.register({ key: "q", description: "x", run: () => undefined })).toThrow();
    expect(() => r.register({ key: "q n", description: "x", run: () => undefined })).toThrow();
    r.register({ key: "w", description: "x", run: () => undefined });
    expect(() => r.register({ key: "w c", description: "x", run: () => undefined })).toThrow();
  });

  test("the keys of the console are all allowed, and the browser's own are not", () => {
    for (const k of ["q n", "w c", "]", "["]) {
      expect(refusal(k)).toBeNull();
    }
    for (const k of ["ctrl+t", "ctrl+w", "ctrl+n", "meta+t", "meta+w", "meta+n", "mod+t"]) {
      expect(refusal(k) === null).toBe(false);
    }
  });
});
