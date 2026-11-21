import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement, ReactNode } from "react";
import { CloseIcon } from "../components/icons";
import { ShortcutRegistry } from "./registry";
import type { Listed, Shortcut } from "./registry";

interface ShortcutsApi {
  registry: ShortcutRegistry;
  openHelp: () => void;
}

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
  { key: "Ctrl+Home, Ctrl+End", description: "First and last cell of the grid (End asks for the exact count)" },
  { key: "Page Up, Page Down", description: "Move a screen of rows" },
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

function HelpDialog({ registry, open, onClose }: { registry: ShortcutRegistry; open: boolean; onClose: () => void }): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const [listed, setListed] = useState<Listed[]>([]);

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (open && !d.open) {
      setListed(registry.list());
      d.showModal();
    } else if (!open && d.open) {
      d.close();
    }
  }, [open, registry]);

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby="shortcut-title" onClose={onClose}>
      <header className="dialog-header">
        <h2 id="shortcut-title" className="dialog-title">
          Keyboard shortcuts
        </h2>
        <button type="button" className="icon-button" aria-label="Close the shortcut help" onClick={onClose}>
          <CloseIcon />
        </button>
      </header>
      <p className="muted">Shortcuts do nothing while you type in a field.</p>
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
  const [helpOpen, setHelpOpen] = useState(false);
  const api = useMemo<ShortcutsApi>(() => ({ registry, openHelp: () => setHelpOpen(true) }), [registry]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent): void => {
      // Without this the "/" that moved the focus would be typed into the field it moved to.
      if (registry.handle(e)) {
        e.preventDefault();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [registry]);

  return (
    <ShortcutsContext.Provider value={api}>
      <HelpShortcut />
      {children}
      <HelpDialog registry={registry} open={helpOpen} onClose={() => setHelpOpen(false)} />
    </ShortcutsContext.Provider>
  );
}

function HelpShortcut(): null {
  const openHelp = useShortcutHelp();
  useShortcut({ key: "?", description: "Show this help", run: openHelp });
  return null;
}
