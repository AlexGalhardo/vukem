import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import type { StatementInfo } from "./classify";

export interface PendingWrite {
  statement: string;
  info: StatementInfo;
}

/**
 * ConfirmWriteDialog asks before a write runs. It shows the statement and the
 * source it will change, and the button is named for the statement ("Run
 * UPDATE"). A DROP, a TRUNCATE and an UPDATE or DELETE with no WHERE also ask
 * for the name of the source to be typed. It is a native modal dialog: focus
 * stays inside it, Escape cancels, and focus goes back to where it was.
 */
export function ConfirmWriteDialog({
  pending,
  source,
  note,
  onConfirm,
  onCancel,
  onAfterClose,
}: {
  pending: PendingWrite | null;
  source: string;
  /** What to say when no name has to be typed; SQL says it runs in a transaction. */
  note?: string;
  onConfirm: () => void;
  onCancel: () => void;
  /** Runs right after the dialog closed, to put focus back: the browser's own restore can leave the caret reset. */
  onAfterClose: () => void;
}): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const [typed, setTyped] = useState("");
  const id = useId();
  const afterClose = useRef(onAfterClose);
  afterClose.current = onAfterClose;

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (pending && !d.open) {
      setTyped("");
      d.showModal();
      // The first stop is the typed name, or Cancel: never the button that runs the statement.
      d.querySelector<HTMLElement>("[data-first-focus]")?.focus();
    } else if (!pending && d.open) {
      d.close();
      afterClose.current();
    }
  }, [pending]);

  const need = pending?.info.destructive === true;
  const ready = !need || typed === source;

  const submit = (e: FormEvent): void => {
    e.preventDefault();
    if (ready) {
      onConfirm();
    }
  };

  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-labelledby={`${id}-title`}
      onCancel={(e) => {
        // Escape: let the parent close it, so the focus return and any next dialog stay in order.
        e.preventDefault();
        onCancel();
      }}
      onClick={(e) => {
        // A click on the backdrop lands on the dialog element itself.
        if (e.target === dialog.current) {
          onCancel();
        }
      }}
    >
      <form onSubmit={submit}>
        <h2 id={`${id}-title`} className="dialog-title">
          Run {pending?.info.kind} on {source}?
        </h2>
        <pre className="confirm-statement" tabIndex={0} aria-label="Statement to run">
          {pending?.statement}
        </pre>
        {need ? (
          <>
            <p className="confirm-warning" role="alert">
              {pending?.info.warning}
            </p>
            <label className="new-label" htmlFor={`${id}-name`}>
              Type {source} to confirm
            </label>
            <input
              id={`${id}-name`}
              className="dialog-input"
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={typed}
              data-first-focus=""
              onChange={(e) => setTyped(e.target.value)}
            />
          </>
        ) : (
          <p className="muted">{note ?? "It runs in a transaction that is committed only if the statement succeeds."}</p>
        )}
        <div className="dialog-actions">
          <button type="button" className="button" data-first-focus={need ? undefined : ""} onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className={need ? "button button-danger" : "button button-primary"} disabled={!ready}>
            Run {pending?.info.kind}
          </button>
        </div>
      </form>
    </dialog>
  );
}
