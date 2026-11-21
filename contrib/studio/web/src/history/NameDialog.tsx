import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactElement } from "react";

export interface NameRequest {
  title: string;
  label: string;
  initial: string;
  confirmLabel: string;
}

/**
 * NameDialog asks for one name in a native modal dialog (saving or renaming a
 * query). The parent answers with an error text to keep it open, or closes it
 * by clearing the request. Escape cancels; focus starts in the field.
 */
export function NameDialog({
  request,
  error,
  onConfirm,
  onCancel,
  onAfterClose,
}: {
  request: NameRequest | null;
  error: string;
  onConfirm: (name: string) => void;
  onCancel: () => void;
  onAfterClose: () => void;
}): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const afterClose = useRef(onAfterClose);
  afterClose.current = onAfterClose;
  const [name, setName] = useState("");
  const id = useId();

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (request && !d.open) {
      setName(request.initial);
      d.showModal();
      input.current?.focus();
      input.current?.select();
    } else if (!request && d.open) {
      d.close();
      afterClose.current();
    }
  }, [request]);

  const submit = (e: FormEvent): void => {
    e.preventDefault();
    onConfirm(name);
  };

  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-labelledby={`${id}-title`}
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
      <form onSubmit={submit}>
        <h2 id={`${id}-title`} className="dialog-title">
          {request?.title}
        </h2>
        <label className="new-label" htmlFor={`${id}-name`}>
          {request?.label}
        </label>
        <input
          id={`${id}-name`}
          ref={input}
          className="dialog-input"
          type="text"
          autoComplete="off"
          maxLength={80}
          value={name}
          aria-invalid={error !== ""}
          aria-describedby={error !== "" ? `${id}-error` : undefined}
          onChange={(e) => setName(e.target.value)}
        />
        {error !== "" ? (
          <p id={`${id}-error`} className="confirm-warning" role="alert">
            {error}
          </p>
        ) : null}
        <div className="dialog-actions">
          <button type="button" className="button" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="button button-primary" disabled={name.trim() === ""}>
            {request?.confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  );
}
