"use client";

import { useId, useState } from "react";
import { Tooltip } from "@base-ui/react/tooltip";

/**
 * Inline term with a definition tooltip: opens on hover, keyboard focus
 * and tap (touch devices have no hover, so a click opens it too). The
 * definition is also linked with aria-describedby, since the Base UI popup
 * is visual only.
 */
export function Term({ tip, children }: { tip: string; children: React.ReactNode }): React.ReactElement {
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <>
      <Tooltip.Root open={open} onOpenChange={setOpen}>
        <Tooltip.Trigger
          closeOnClick={false}
          delay={200}
          onClick={() => setOpen(true)}
          render={<span tabIndex={0} className="term" aria-describedby={id} />}
        >
          {children}
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Positioner sideOffset={6} collisionPadding={12}>
            <Tooltip.Popup className="max-w-72 rounded-md border border-border bg-bg px-2.5 py-1.5 text-xs leading-relaxed text-fg shadow-sm transition-opacity data-ending-style:opacity-0 data-starting-style:opacity-0">
              {tip}
            </Tooltip.Popup>
          </Tooltip.Positioner>
        </Tooltip.Portal>
      </Tooltip.Root>
      <span id={id} hidden>
        {tip}
      </span>
    </>
  );
}
