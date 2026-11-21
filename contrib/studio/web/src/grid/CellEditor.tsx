import { useEffect, useLayoutEffect, useRef } from "react";
import type { KeyboardEvent, ReactElement } from "react";
import type { Draft } from "./editState";
import type { CellEdit } from "./useCellEdit";

interface CellEditorProps {
  edit: CellEdit;
  /** Finds the DOM element of the cell being edited, to sit over it. */
  find: () => HTMLElement | null;
}

const minWidth = 240;

function DraftText({ draft }: { draft: Draft }): ReactElement {
  return draft.null ? <span className="token token-null">NULL</span> : <pre className="drawer-value">{draft.text}</pre>;
}

/**
 * CellEditor is the editor of one cell. It is a manual popover (in the top
 * layer, so the cell's own clipping cannot cut it) placed over the cell it
 * edits, with the editor of the column's type and visible Save, Cancel and
 * Set to NULL buttons. Enter saves, Escape cancels, Tab saves and moves right.
 */
export function CellEditor({ edit, find }: CellEditorProps): ReactElement {
  const panel = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement & HTMLTextAreaElement & HTMLSelectElement>(null);
  const { state } = edit;
  const open = state.phase === "loading" || state.phase === "editing" || state.phase === "saving";
  const focusedFor = useRef<string>("");

  // Placed on every render: the grid scrolls and the cell moves under the editor.
  useLayoutEffect(() => {
    const el = panel.current;
    if (!el) {
      return;
    }
    const shown = el.matches(":popover-open");
    if (open && !shown) {
      el.showPopover();
    } else if (!open && shown) {
      el.hidePopover();
    }
    const anchor = open ? find() : null;
    if (anchor) {
      const rect = anchor.getBoundingClientRect();
      const width = Math.max(rect.width, minWidth);
      el.style.top = `${Math.round(rect.top)}px`;
      el.style.left = `${Math.round(Math.min(rect.left, Math.max(window.innerWidth - width - 8, 0)))}px`;
      el.style.minWidth = `${Math.round(width)}px`;
    }
  });

  const token = state.phase === "editing" ? `${state.at.row}:${state.at.col}` : "";
  useEffect(() => {
    if (token !== "" && focusedFor.current !== token) {
      focusedFor.current = token;
      field.current?.focus();
      field.current?.select?.();
    } else if (token === "") {
      focusedFor.current = "";
    }
  }, [token]);

  const at = state.phase === "idle" || state.phase === "conflict" ? null : state.at;
  const editing = state.phase === "editing" || state.phase === "saving" ? state : null;
  const draft = editing?.draft;
  const saving = state.phase === "saving";

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    // Keys typed in the editor are the editor's, not the grid's or a shortcut's.
    e.stopPropagation();
    if (!editing || saving) {
      return;
    }
    const multiline = e.target instanceof HTMLTextAreaElement;
    if (e.key === "Escape") {
      e.preventDefault();
      edit.cancel();
    } else if (e.key === "Enter" && (!multiline || e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      edit.commit();
    } else if (e.key === "Tab" && !e.shiftKey && !multiline) {
      e.preventDefault();
      edit.commit("right");
    }
  };

  const text = draft?.null ? "" : (draft?.text ?? "");
  const wide = at?.kind === "json" || text.includes("\n") || text.length > 80;

  let control: ReactElement | null = null;
  if (editing && at) {
    const label = `Edit ${at.column}`;
    if (at.kind === "boolean") {
      const current = draft?.null ? "null" : ["true", "1"].includes(text.trim().toLowerCase()) ? "true" : "false";
      control = (
        <select
          ref={field}
          className="editor-field"
          aria-label={label}
          value={current}
          disabled={saving}
          onChange={(e) => (e.target.value === "null" ? edit.setNull() : edit.input(e.target.value))}
        >
          <option value="true">true</option>
          <option value="false">false</option>
          {at.nullable ? <option value="null">NULL</option> : null}
        </select>
      );
    } else if (wide) {
      control = (
        <textarea
          ref={field}
          className="editor-field editor-text"
          aria-label={label}
          rows={at.kind === "json" ? 8 : 5}
          spellCheck={false}
          value={text}
          disabled={saving}
          onChange={(e) => edit.input(e.target.value)}
        />
      );
    } else {
      control = (
        <input
          ref={field}
          className="editor-field"
          type="text"
          aria-label={label}
          inputMode={at.kind === "number" ? "decimal" : undefined}
          autoComplete="off"
          spellCheck={false}
          placeholder={draft?.null ? "NULL" : at.kind === "date" ? "2026-01-02 or 2026-01-02T03:04:05Z" : undefined}
          value={text}
          disabled={saving}
          onChange={(e) => edit.input(e.target.value)}
        />
      );
    }
  }

  return (
    <div
      ref={panel}
      popover="manual"
      role="group"
      aria-label={at ? `Editor for ${at.column}` : "Cell editor"}
      className="cell-editor"
      onKeyDown={onKeyDown}
    >
      {state.phase === "loading" ? <p className="muted">Reading the full value…</p> : null}
      {control}
      {editing?.phase === "editing" && editing.error ? (
        <p className="editor-error" role="alert">
          {editing.error}
        </p>
      ) : null}
      {editing ? (
        <div className="editor-actions">
          <button type="button" className="button button-small" onClick={() => edit.commit()} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
          <button type="button" className="button button-small" onClick={edit.cancel} disabled={saving}>
            Cancel
          </button>
          {at?.nullable && at.kind !== "boolean" ? (
            <button type="button" className="button button-small" onClick={edit.setNull} disabled={saving}>
              Set to NULL
            </button>
          ) : null}
          <span className="muted editor-hint">{wide ? "Ctrl+Enter saves" : "Enter saves, Esc cancels"}</span>
        </div>
      ) : null}
    </div>
  );
}

/** ConflictDialog asks what to do when the row changed since the editor read it. */
export function ConflictDialog({ edit }: { edit: CellEdit }): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const { state } = edit;
  const conflict = state.phase === "conflict" ? state : null;

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (conflict && !d.open) {
      d.showModal();
    } else if (!conflict && d.open) {
      d.close();
    }
  }, [conflict]);

  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-labelledby="conflict-title"
      onClose={() => {
        // Escape closes the dialog without a choice: the safe reading is to keep what the database holds.
        if (conflict) {
          edit.takeTheirs();
        }
      }}
    >
      <h2 id="conflict-title" className="dialog-title">
        This value changed in the database
      </h2>
      {conflict ? (
        <>
          <p>
            Someone changed <span className="mono">{conflict.at.column}</span> since you started editing. Nothing was
            saved.
          </p>
          <h3 className="dialog-subtitle">The database now has</h3>
          <DraftText draft={conflict.theirs} />
          <h3 className="dialog-subtitle">You typed</h3>
          <DraftText draft={conflict.mine} />
          <div className="dialog-actions">
            <button type="button" className="button" onClick={edit.takeTheirs}>
              Take theirs
            </button>
            <button type="button" className="button button-primary" onClick={edit.keepMine}>
              Keep mine
            </button>
          </div>
        </>
      ) : null}
    </dialog>
  );
}
