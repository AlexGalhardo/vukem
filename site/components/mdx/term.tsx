import { useId } from "react";

/**
 * Inline term with a definition. The tip shows on hover, keyboard focus
 * and tap, and stays available to assistive tech through aria-describedby.
 */
export function Term({ tip, children }: { tip: string; children: React.ReactNode }): React.ReactElement {
  const id = useId();
  return (
    <span className="term" tabIndex={0} aria-describedby={id}>
      {children}
      <span id={id} role="tooltip" className="term-tip">
        {tip}
      </span>
    </span>
  );
}
