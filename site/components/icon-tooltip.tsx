"use client";

import { Tooltip } from "@base-ui/react/tooltip";

/** Wraps a single interactive element (passed as `render`) with a tooltip. */
export function IconTooltip({ label, children }: { label: string; children: React.ReactElement }) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner sideOffset={8}>
          <Tooltip.Popup className="rounded-md border border-border bg-bg px-2 py-1 text-xs text-fg shadow-sm transition-opacity data-ending-style:opacity-0 data-starting-style:opacity-0">
            {label}
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
