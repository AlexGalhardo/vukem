import { useEffect, useId, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import { ApiError, apiPath, apiSend } from "../api/client";
import type { TableDetail } from "../api/types";
import { CloseIcon } from "../components/icons";
import { useModal } from "../components/useModal";
import { buildInsert } from "./editState";
import type { NewField } from "./editState";
import { columnKind } from "./filters";
import { confirmText, packKey } from "./rowKey";

function messageOf(err: unknown): string {
  return err instanceof ApiError ? err.message : "Studio could not be reached.";
}

interface NewRowProps {
  source: string;
  table: string;
  detail: TableDetail;
  open: boolean;
  onClose: () => void;
  /** The row was added. */
  onDone: () => void;
}

function initialFields(detail: TableDetail): Record<string, NewField> {
  const out: Record<string, NewField> = {};
  for (const c of detail.columns) {
    const needsValue = c.default === undefined && !c.nullable && !detail.primaryKey.includes(c.name);
    out[c.name] = { mode: needsValue ? "value" : "default", text: "" };
  }
  return out;
}

/** NewRowDialog is the form for one new row: per column, the default, NULL or a typed value. */
export function NewRowDialog({ source, table, detail, open, onClose, onDone }: NewRowProps): ReactElement {
  const dialog = useModal(open, onClose);
  const [fields, setFields] = useState(() => initialFields(detail));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = useId();

  useEffect(() => {
    if (open) {
      setFields(initialFields(detail));
      setErrors({});
      setFailure(null);
    }
  }, [open, detail]);

  const set = (name: string, patch: Partial<NewField>): void =>
    setFields((prev) => ({ ...prev, [name]: { mode: "value", text: "", ...prev[name], ...patch } }));

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    const plan = buildInsert(detail.columns, fields, detail.primaryKey);
    setErrors(plan.errors);
    if (Object.keys(plan.errors).length > 0) {
      return;
    }
    setBusy(true);
    setFailure(null);
    apiSend("POST", apiPath("sources", source, "tables", table, "rows"), plan.body).then(
      () => {
        setBusy(false);
        onDone();
      },
      (err: unknown) => {
        setBusy(false);
        setFailure(messageOf(err));
      },
    );
  };

  return (
    <dialog ref={dialog} className="drawer" aria-labelledby={`${id}-title`}>
      <header className="drawer-header">
        <h2 id={`${id}-title`} className="drawer-title">
          New row in {table}
        </h2>
        <button type="button" className="icon-button" aria-label="Close the form" onClick={onClose}>
          <CloseIcon />
        </button>
      </header>
      <form className="drawer-body" onSubmit={submit} noValidate>
        <p className="muted">
          Columns left on &ldquo;Default&rdquo; are not sent: the database fills them. This adds a real row; Studio
          cannot undo it.
        </p>
        {detail.columns.map((c) => {
          const f = fields[c.name] ?? { mode: "default", text: "" };
          const fieldId = `${id}-${c.name}`;
          const kind = columnKind(c.type);
          const error = errors[c.name];
          return (
            <div key={c.name} className="new-field">
              <label htmlFor={fieldId} className="new-label">
                <span className="mono">{c.name}</span>
                <span className="hcell-type"> {c.type}</span>
                {c.nullable ? null : <span className="muted"> required</span>}
                {c.default !== undefined ? <span className="muted"> default {c.default}</span> : null}
              </label>
              <div className="new-controls">
                <select
                  aria-label={`How to fill ${c.name}`}
                  value={f.mode}
                  onChange={(e) => set(c.name, { mode: e.target.value as NewField["mode"] })}
                >
                  <option value="default">Default</option>
                  {c.nullable ? <option value="null">NULL</option> : null}
                  <option value="value">Value</option>
                </select>
                {kind === "json" ? (
                  <textarea
                    id={fieldId}
                    rows={3}
                    spellCheck={false}
                    value={f.text}
                    disabled={f.mode !== "value"}
                    aria-invalid={error ? true : undefined}
                    aria-describedby={error ? `${fieldId}-error` : undefined}
                    onChange={(e) => set(c.name, { text: e.target.value })}
                  />
                ) : (
                  <input
                    id={fieldId}
                    type="text"
                    autoComplete="off"
                    spellCheck={false}
                    value={f.text}
                    disabled={f.mode !== "value"}
                    aria-invalid={error ? true : undefined}
                    aria-describedby={error ? `${fieldId}-error` : undefined}
                    onChange={(e) => set(c.name, { text: e.target.value })}
                  />
                )}
              </div>
              {error ? (
                <p id={`${fieldId}-error`} className="editor-error">
                  {error}
                </p>
              ) : null}
            </div>
          );
        })}
        {failure ? (
          <p className="editor-error" role="alert">
            {failure}
          </p>
        ) : null}
        <div className="dialog-actions">
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="button button-primary" disabled={busy}>
            {busy ? "Adding…" : "Add row"}
          </button>
        </div>
      </form>
    </dialog>
  );
}

interface DeleteProps {
  source: string;
  table: string;
  /** The primary key of the row to delete, or null when no dialog is open. */
  keyParts: string[] | null;
  onClose: () => void;
  onDone: () => void;
}

/** DeleteDialog asks for the key to be typed before it deletes one row. */
export function DeleteDialog({ source, table, keyParts, onClose, onDone }: DeleteProps): ReactElement {
  const dialog = useModal(keyParts !== null, onClose);
  const [typed, setTyped] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = useId();
  const expected = keyParts ? confirmText(keyParts) : "";

  useEffect(() => {
    setTyped("");
    setFailure(null);
  }, [keyParts]);

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!keyParts || typed !== expected) {
      return;
    }
    setBusy(true);
    apiSend("DELETE", apiPath("sources", source, "tables", table, "rows", packKey(keyParts)), { confirm: typed }).then(
      () => {
        setBusy(false);
        onDone();
      },
      (err: unknown) => {
        setBusy(false);
        setFailure(messageOf(err));
      },
    );
  };

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="dialog-title">
        Delete this row?
      </h2>
      <form onSubmit={submit}>
        <p>
          This deletes the row of <span className="mono">{table}</span> whose key is{" "}
          <strong className="mono" data-testid="delete-key">
            {expected}
          </strong>
          . It is permanent: Studio has no undo.
        </p>
        <label htmlFor={`${id}-confirm`}>Type the key to confirm</label>
        <input
          id={`${id}-confirm`}
          className="dialog-input"
          type="text"
          autoComplete="off"
          spellCheck={false}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
        />
        {failure ? (
          <p className="editor-error" role="alert">
            {failure}
          </p>
        ) : null}
        <div className="dialog-actions">
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="button button-danger" disabled={busy || typed !== expected || expected === ""}>
            {busy ? "Deleting…" : "Delete row"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
