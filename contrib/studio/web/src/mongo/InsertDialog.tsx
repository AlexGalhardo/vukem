import { useEffect, useId, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import { ApiError, apiPath, apiSend } from "../api/client";
import { useModal } from "../components/useModal";
import { parseJSON } from "./json";

function problemOf(text: string): string | null {
  const r = parseJSON(text);
  if (!r.ok) {
    return `Not valid JSON: ${r.error.message} (line ${r.error.line}, column ${r.error.column}).`;
  }
  if (r.value === null || typeof r.value !== "object" || Array.isArray(r.value)) {
    return "A document is one JSON object.";
  }
  return null;
}

/** InsertDialog adds one document. The text is checked as JSON before it is sent; the server checks it again. */
export function InsertDialog({
  source,
  collection,
  open,
  onClose,
  onDone,
}: {
  source: string;
  collection: string;
  open: boolean;
  onClose: () => void;
  onDone: () => void;
}): ReactElement {
  const dialog = useModal(open, onClose);
  const [text, setText] = useState("{\n  \n}");
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = useId();
  const problem = problemOf(text);

  useEffect(() => {
    if (open) {
      setText("{\n  \n}");
      setFailure(null);
    }
  }, [open]);

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (problem !== null) return;
    setBusy(true);
    apiSend("POST", apiPath("sources", source, "collections", collection, "documents"), { doc: text }).then(
      () => {
        setBusy(false);
        onDone();
      },
      (err: unknown) => {
        setBusy(false);
        setFailure(err instanceof ApiError ? err.message : "Studio could not be reached.");
      },
    );
  };

  return (
    <dialog ref={dialog} className="dialog dialog-wide" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="dialog-title">
        Insert a document into {collection}
      </h2>
      <form onSubmit={submit}>
        <label htmlFor={`${id}-doc`}>Document (Extended JSON)</label>
        <textarea
          id={`${id}-doc`}
          className="dialog-text doc-textarea"
          rows={14}
          spellCheck={false}
          value={text}
          aria-invalid={problem ? true : undefined}
          aria-describedby={problem ? `${id}-problem` : undefined}
          onChange={(e) => setText(e.target.value)}
        />
        {problem ? (
          <p id={`${id}-problem`} className="editor-error" role="alert">
            {problem}
          </p>
        ) : null}
        {failure ? (
          <p className="editor-error" role="alert">
            {failure}
          </p>
        ) : null}
        <div className="dialog-actions">
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="button button-primary" disabled={busy || problem !== null}>
            {busy ? "Inserting…" : "Insert document"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
