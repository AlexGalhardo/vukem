import { useEffect, useId, useState } from "react";
import type { FormEvent, ReactElement, ReactNode } from "react";
import { ApiError } from "../api/client";
import { useModal } from "./useModal";

export interface TypedConfirmProps {
  open: boolean;
  title: string;
  /** What is about to happen, in a sentence or two. */
  children: ReactNode;
  /** What the person must type, exactly: the key, the _id. */
  expected: string;
  /** Names the thing typed, for the label: "the key", "the _id". */
  what: string;
  confirmLabel: string;
  danger?: boolean;
  onClose: () => void;
  /** Runs the change. A rejection shows its message and leaves the dialog open. */
  onConfirm: () => Promise<void>;
}

function messageOf(err: unknown): string {
  return err instanceof ApiError ? err.message : "Studio could not be reached.";
}

/**
 * TypedConfirm is the confirmation for a change that cannot be undone: the
 * button stays off until what the person types equals what the change targets.
 */
export function TypedConfirm({ open, title, children, expected, what, confirmLabel, danger, onClose, onConfirm }: TypedConfirmProps): ReactElement {
  const dialog = useModal(open, onClose);
  const [typed, setTyped] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = useId();

  useEffect(() => {
    if (open) {
      setTyped("");
      setFailure(null);
      setBusy(false);
    }
  }, [open]);

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (typed !== expected || expected === "") {
      return;
    }
    setBusy(true);
    onConfirm().then(
      () => setBusy(false),
      (err: unknown) => {
        setBusy(false);
        setFailure(messageOf(err));
      },
    );
  };

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="dialog-title">
        {title}
      </h2>
      <form onSubmit={submit}>
        <div className="dialog-text-block">{children}</div>
        <p>
          <strong className="mono" data-testid="confirm-expected">
            {expected}
          </strong>
        </p>
        <label htmlFor={`${id}-confirm`}>Type {what} to confirm</label>
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
          <button type="submit" className={danger ? "button button-danger" : "button button-primary"} disabled={busy || typed !== expected || expected === ""}>
            {busy ? "Working…" : confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  );
}
