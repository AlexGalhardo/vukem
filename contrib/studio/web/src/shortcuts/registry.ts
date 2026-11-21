/**
 * A small registry of Studio's keyboard shortcuts, written for this project.
 *
 * The rules are those of a careful web app: a shortcut is one plain key (or two
 * in a row, a chord such as "q n"), never a combination the browser or the
 * system owns, and it never fires while the person is typing in a field.
 */

export interface Shortcut {
  /** The key as event.key reports it: "/", "r", "n", "?". Two keys in a row are written with a space: "q n". */
  key: string;
  /** What it does, for the help dialog. */
  description: string;
  run: () => void;
  /** When false the shortcut does nothing and the help does not list it. */
  enabled?: () => boolean;
}

export interface KeyEventLike {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat: boolean;
  isComposing: boolean;
  defaultPrevented: boolean;
  target: unknown;
}

const chordPattern = /^(\S) (\S)$/;

/** How long the second key of a chord may take. */
export const chordTimeoutMs = 1500;

/** Why a key cannot be a Studio shortcut, or null when it can. */
export function refusal(key: string): string | null {
  const chord = chordPattern.exec(key);
  if (chord) {
    return refusal(chord[1] as string) ?? refusal(chord[2] as string);
  }

  const parts = key.split("+");
  const base = parts[parts.length - 1] ?? "";
  const mods = parts.slice(0, -1).map((m) => m.toLowerCase());

  if (key === "" || base === "") {
    return "a shortcut needs a key";
  }
  if (mods.some((m) => m === "ctrl" || m === "control" || m === "meta" || m === "cmd" || m === "alt" || m === "option")) {
    return "combinations with Ctrl, Cmd or Alt belong to the browser and the system";
  }
  if (mods.length > 0) {
    return "write the key as the browser reports it (for example ?), not with modifiers";
  }
  if (/^f\d{1,2}$/i.test(base)) {
    return "function keys are reserved by the browser";
  }
  if (["Tab", "Escape", "Enter", " ", "Space", "Backspace", "Delete", "ContextMenu"].includes(base)) {
    return `${base} keeps its meaning in forms and in the grid`;
  }
  if (base.startsWith("Arrow") || ["Home", "End", "PageUp", "PageDown"].includes(base)) {
    return "movement keys belong to the grid and to scrolling";
  }
  if ([...base].length !== 1) {
    return "a shortcut is a single character";
  }
  return null;
}

const fieldSelector = "input, textarea, select, [contenteditable]:not([contenteditable='false'])";

interface TargetLike {
  closest?: (selector: string) => unknown;
  isContentEditable?: boolean;
}

/** isTypingTarget is true for a field where keys must reach the field, not a shortcut. */
export function isTypingTarget(target: unknown): boolean {
  if (typeof target !== "object" || target === null) {
    return false;
  }
  const el = target as TargetLike;
  if (typeof el.closest === "function" && el.closest(fieldSelector) !== null) {
    return true;
  }
  return el.isContentEditable === true;
}

/** A shortcut as the help dialog lists it. */
export interface Listed {
  key: string;
  description: string;
}

export class ShortcutRegistry {
  private readonly shortcuts = new Map<string, Shortcut>();
  private pending: { key: string; at: number } | null = null;

  constructor(private readonly now: () => number = () => Date.now()) {}

  /** register adds a shortcut and returns the function that removes it. It throws for a refused key. */
  register(shortcut: Shortcut): () => void {
    const why = refusal(shortcut.key);
    if (why !== null) {
      throw new Error(`shortcut "${shortcut.key}" refused: ${why}`);
    }
    if (this.shortcuts.has(shortcut.key)) {
      throw new Error(`shortcut "${shortcut.key}" is already taken`);
    }
    const chord = chordPattern.exec(shortcut.key);
    if (chord && this.shortcuts.has(chord[1] as string)) {
      throw new Error(`shortcut "${shortcut.key}" starts with "${chord[1]}", which is a shortcut by itself`);
    }
    if (!chord && [...this.shortcuts.keys()].some((k) => k.startsWith(shortcut.key + " "))) {
      throw new Error(`shortcut "${shortcut.key}" is the first key of a chord`);
    }
    this.shortcuts.set(shortcut.key, shortcut);
    return () => {
      if (this.shortcuts.get(shortcut.key) === shortcut) {
        this.shortcuts.delete(shortcut.key);
      }
    };
  }

  /** list returns the shortcuts that are enabled now, in the order they were added. */
  list(): Listed[] {
    return [...this.shortcuts.values()]
      .filter((s) => s.enabled?.() !== false)
      .map((s) => ({ key: s.key, description: s.description }));
  }

  /**
   * handle runs the shortcut for a key event and says whether it did. It does
   * nothing for a modified key, an auto-repeat, an IME composition, an event
   * something already handled, or a key typed in a field.
   */
  handle(event: KeyEventLike): boolean {
    if (
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.repeat ||
      event.isComposing ||
      event.defaultPrevented ||
      isTypingTarget(event.target)
    ) {
      this.pending = null;
      return false;
    }

    const now = this.now();
    const first = this.pending !== null && now - this.pending.at <= chordTimeoutMs ? this.pending.key : null;
    this.pending = null;

    if (first !== null) {
      const chord = this.shortcuts.get(`${first} ${event.key}`);
      if (chord && chord.enabled?.() !== false) {
        chord.run();
        return true;
      }
    }

    const shortcut = this.shortcuts.get(event.key);
    if (shortcut && shortcut.enabled?.() !== false) {
      shortcut.run();
      return true;
    }

    // The first key of a chord is held for the second.
    const starts = [...this.shortcuts.values()].some((s) => s.key.startsWith(event.key + " ") && s.enabled?.() !== false);
    if (starts) {
      this.pending = { key: event.key, at: now };
      return true;
    }
    return false;
  }
}
