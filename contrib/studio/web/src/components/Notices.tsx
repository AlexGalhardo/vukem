import type { ReactElement } from "react";
import type { ApiError } from "../api/client";

export function Loading({ what }: { what: string }): ReactElement {
  return (
    <p className="muted" role="status">
      Loading {what}…
    </p>
  );
}

export function ErrorNotice({ error, onRetry }: { error: ApiError; onRetry?: () => void }): ReactElement {
  return (
    <div className="notice notice-error" role="alert">
      <p>{error.message}</p>
      {onRetry && error.status !== 401 ? (
        <button type="button" className="button" onClick={onRetry}>
          Try again
        </button>
      ) : null}
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: string }): ReactElement {
  return (
    <div className="empty">
      <p className="empty-title">{title}</p>
      {children ? <p className="muted">{children}</p> : null}
    </div>
  );
}
