import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement, ReactNode } from "react";
import { CloseIcon } from "../components/icons";
import { helpChordLabel, isApplePlatform, isHelpChord, ShortcutRegistry } from "./registry";
import type { Listed, Shortcut } from "./registry";

interface ShortcutsApi {
  registry: ShortcutRegistry;
  openHelp: () => void;
}

const apple = typeof navigator !== "undefined" && isApplePlatform(navigator.platform);

/** The chord that opens the help, written for this platform: "Ctrl K" or "⌘ K". */
export const helpChord = helpChordLabel(apple);

const ShortcutsContext = createContext<ShortcutsApi | null>(null);

function useShortcutsApi(): ShortcutsApi {
  const api = useContext(ShortcutsContext);
  if (!api) {
    throw new Error("ShortcutsProvider is missing");
  }
  return api;
}

/** useShortcutHelp returns the function that opens the help dialog. */
export function useShortcutHelp(): () => void {
  return useShortcutsApi().openHelp;
}

/**
 * useShortcut registers a shortcut for as long as the component is mounted.
 * The latest run and enabled functions are used, so closures stay fresh
 * without registering again on every render.
 */
export function useShortcut(shortcut: Shortcut): void {
  const { registry } = useShortcutsApi();
  const latest = useRef(shortcut);
  latest.current = shortcut;

  useEffect(
    () =>
      registry.register({
        key: shortcut.key,
        description: shortcut.description,
        run: () => latest.current.run(),
        enabled: () => latest.current.enabled?.() !== false,
      }),
    [registry, shortcut.key, shortcut.description],
  );
}

/** The keys the grid handles itself, listed in the help next to the registered shortcuts. */
export const gridKeys: readonly Listed[] = [
  { key: "Arrow keys", description: "Move between cells" },
  { key: "Home, End", description: "First and last cell of the row" },
  { key: "Ctrl+Home, Ctrl+End", description: "First and last row of the whole table: the first page, and the last page" },
  { key: "Page Up, Page Down", description: "Move a screen of rows, inside the page" },
  { key: "Enter", description: "Sort on a header; open the row on a cell" },
  { key: "F2", description: "Edit the cell (write mode)" },
  { key: "Escape", description: "Cancel an edit or clear the selection" },
  { key: "Space", description: "Select or unselect the row" },
  { key: "Shift+Arrow", description: "Extend the selection" },
  { key: "Ctrl+C, Cmd+C", description: "Copy the cell, or the selected rows as tab-separated text" },
  { key: "Shift+F10, Menu key", description: "Open the menu of the row or the column" },
];

/** The keys of the SQL editor. It is a typing field, so only these chords apply inside it and none of the page shortcuts. */
export const editorKeys: readonly Listed[] = [
  { key: "Ctrl+Enter, Cmd+Enter", description: "Run the selected text, or the statement at the cursor" },
  { key: "Ctrl+Shift+Enter, Cmd+Shift+Enter", description: "Run all the text (or the selection) as one statement" },
  { key: "Ctrl+/, Cmd+/", description: "Comment or uncomment the lines" },
  { key: "Ctrl+S, Cmd+S", description: "Save the query to the library (only while the focus is in the editor)" },
  { key: "Ctrl+F, Cmd+F", description: "Find in the query (only while the focus is in the editor)" },
  { key: "Ctrl+Space", description: "Show completions" },
  { key: "Tab, Shift+Tab", description: "Indent and outdent, or accept a completion" },
  { key: "Enter, Arrow Up, Arrow Down", description: "Accept or move through the completions" },
  { key: "Escape", description: "Close the completions or the find bar; then Tab leaves the editor" },
];

/** The keys of the tab strip, when a tab has the focus. */
export const tabKeys: readonly Listed[] = [
  { key: "Arrow Left, Arrow Right", description: "Move to the previous or next tab" },
  { key: "Home, End", description: "First and last tab" },
  { key: "Alt+Arrow Left, Alt+Arrow Right", description: "Move the tab" },
  { key: "Delete", description: "Close the tab (it asks when the query is not saved)" },
];

function KeyTable({ label, rows }: { label: string; rows: readonly Listed[] }): ReactElement {
  return (
    <table className="data-table" aria-label={label}>
      <tbody>
        {rows.map((s) => (
          <tr key={s.key}>
            <th scope="row">
              <kbd>{s.key}</kbd>
            </th>
            <td>{s.description}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * The dialog opens on a request counter, not on a flag: the native close event
 * arrives after the dialog is already shut, and a flag that still said "open"
 * would swallow a request made in between (Escape, then the chord at once).
 */
function HelpDialog({ registry, request }: { registry: ShortcutRegistry; request: number }): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const [listed, setListed] = useState<Listed[]>([]);

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (request > 0 && !d.open) {
      setListed(registry.list());
      d.showModal();
    }
  }, [request, registry]);

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby="shortcut-title">
      <header className="dialog-header">
        <h2 id="shortcut-title" className="dialog-title">
          Keyboard shortcuts
        </h2>
        <button type="button" className="icon-button" aria-label="Close the shortcut help" onClick={() => dialog.current?.close()}>
          <CloseIcon />
        </button>
      </header>
      <p className="muted">Single-key shortcuts do nothing while you type in a field.</p>
      <h3 className="dialog-subtitle">Anywhere</h3>
      <table className="data-table" aria-label="Shortcuts">
        <tbody>
          {listed.map((s) => (
            <tr key={s.key}>
              <th scope="row">
                <kbd>{s.key.replace(" ", " then ")}</kbd>
              </th>
              <td>{s.description}</td>
            </tr>
          ))}
          <tr>
            <th scope="row">
              <kbd>{helpChord.replace(" ", "+")}</kbd>
            </th>
            <td>Show this help, from anywhere, also while you type</td>
          </tr>
        </tbody>
      </table>
      <h3 className="dialog-subtitle">In the grid</h3>
      <KeyTable label="Grid keys" rows={gridKeys} />
      <h3 className="dialog-subtitle">In the SQL editor</h3>
      <KeyTable label="Editor keys" rows={editorKeys} />
      <h3 className="dialog-subtitle">In the tab strip</h3>
      <KeyTable label="Tab keys" rows={tabKeys} />
    </dialog>
  );
}

/** ShortcutsProvider owns the registry, listens for keys and shows the help dialog. */
export function ShortcutsProvider({ children }: { children: ReactNode }): ReactElement {
  const registry = useMemo(() => new ShortcutRegistry(), []);
  const [helpRequest, setHelpRequest] = useState(0);
  const openHelp = useCallback(() => setHelpRequest((n) => n + 1), []);
  const api = useMemo<ShortcutsApi>(() => ({ registry, openHelp }), [registry, openHelp]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent): void => {
      if (isHelpChord(e, apple)) {
        // The browsers bind Ctrl+K to their own search while the page has focus; the page's own use wins.
        e.preventDefault();
        openHelp();
        return;
      }
      // Without this the "/" that moved the focus would be typed into the field it moved to.
      if (registry.handle(e)) {
        e.preventDefault();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [registry, openHelp]);

  return (
    <ShortcutsContext.Provider value={api}>
      <HelpShortcut />
      {children}
      <HelpDialog registry={registry} request={helpRequest} />
    </ShortcutsContext.Provider>
  );
}

function HelpShortcut(): null {
  const openHelp = useShortcutHelp();
  useShortcut({ key: "?", description: "Show this help", run: openHelp });
  return null;
}
