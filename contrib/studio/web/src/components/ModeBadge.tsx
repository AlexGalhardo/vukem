import type { ReactElement } from "react";
import { modeBadge } from "../lib/mode";

/** ModeBadge always shows whether Studio can change data, in words. */
export function ModeBadge({ write }: { write: boolean }): ReactElement {
  const badge = modeBadge({ write });

  return (
    <span className={`badge badge-${badge.tone}`} title={badge.hint} data-testid="mode-badge">
      {badge.label}
    </span>
  );
}
