import { useEffect, useId, useRef } from "react";
import type { ReactElement } from "react";

export interface ConfirmRequest {
  title: string;
  message: string;
  /** The label of the button that goes ahead. */
  confirmLabel: string;
}

/**
 * ConfirmDialog asks a yes or no question in a native modal dialog: focus is
 * trapped inside, Escape cancels, and the first stop is Cancel, so a stray
 * Enter never goes ahead. onAfterClose puts focus where the caller wants it.
 */
export function ConfirmDialog({
  request,
  onConfirm,
  onCancel,
  onAfterClose,
}: {
  request: ConfirmRequest | null;
  onConfirm: () => void;
  onCancel: () => void;
  onAfterClose: () => void;
}): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const afterClose = useRef(onAfterClose);
  afterClose.current = onAfterClose;
  const id = useId();

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (request && !d.open) {
      d.showModal();
      d.querySelector<HTMLElement>("[data-first-focus]")?.focus();
    } else if (!request && d.open) {
      d.close();
      afterClose.current();
    }
  }, [request]);

  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-labelledby={`${id}-title`}
      aria-describedby={`${id}-text`}
      onCancel={(e) => {
        e.preventDefault();
        onCancel();
      }}
      onClick={(e) => {
        if (e.target === dialog.current) {
          onCancel();
        }
      }}
    >
      <h2 id={`${id}-title`} className="dialog-title">
        {request?.title}
      </h2>
      <p id={`${id}-text`}>{request?.message}</p>
      <div className="dialog-actions">
        <button type="button" className="button" data-first-focus="" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="button button-primary" onClick={onConfirm}>
          {request?.confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
