import { useCallback, useState } from "react";
import type { ReactElement } from "react";

/** useAnnouncer keeps the text of the grid's live region. */
export function useAnnouncer(): { message: string; announce: (text: string) => void } {
  const [message, setMessage] = useState("");

  // The same sentence twice would not be read twice, so a trailing space alternates.
  const announce = useCallback((text: string) => {
    setMessage((prev) => (prev === text ? `${text} ` : text));
  }, []);

  return { message, announce };
}

/** LiveRegion is the one polite status region of the grid: position changes and save results. */
export function LiveRegion({ message }: { message: string }): ReactElement {
  return (
    <div className="visually-hidden" role="status" aria-live="polite" aria-atomic="true" data-testid="grid-live">
      {message}
    </div>
  );
}
