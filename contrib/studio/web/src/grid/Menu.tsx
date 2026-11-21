import { useRef } from "react";
import type { KeyboardEvent, ReactElement, SyntheticEvent } from "react";

export interface MenuItem {
  label: string;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
}

interface MenuProps {
  /** A unique id for the popover element. */
  id: string;
  /** Names the button and the menu: "Actions for row 3". */
  label: string;
  items: readonly MenuItem[];
  /** Called when the menu closes, so the grid can take the focus back. */
  onClose?: () => void;
}

const margin = 4;

/**
 * Menu is a button that opens a native popover with a list of actions. The
 * browser provides the top layer, light dismiss and Escape; this adds arrow
 * key movement and places the popover under its button (through the CSSOM).
 */
export function Menu({ id, label, items, onClose }: MenuProps): ReactElement {
  const panel = useRef<HTMLDivElement>(null);

  const onToggle = (e: SyntheticEvent<HTMLDivElement>): void => {
    const el = panel.current;
    if (!el) {
      return;
    }
    if ((e.nativeEvent as ToggleEvent).newState === "closed") {
      onClose?.();
      return;
    }
    const button = document.querySelector(`[popovertarget="${id}"]`);
    if (button) {
      const rect = button.getBoundingClientRect();
      const width = el.offsetWidth;
      el.style.top = `${Math.round(rect.bottom + margin)}px`;
      el.style.left = `${Math.round(Math.max(margin, Math.min(rect.left, window.innerWidth - width - margin)))}px`;
    }
    el.querySelector<HTMLButtonElement>("button:not([disabled])")?.focus();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    const buttons = [...(panel.current?.querySelectorAll<HTMLButtonElement>("button:not([disabled])") ?? [])];
    const at = buttons.findIndex((b) => b === document.activeElement);
    let next = -1;
    if (e.key === "ArrowDown") {
      next = (at + 1) % buttons.length;
    } else if (e.key === "ArrowUp") {
      next = (at - 1 + buttons.length) % buttons.length;
    } else if (e.key === "Home") {
      next = 0;
    } else if (e.key === "End") {
      next = buttons.length - 1;
    }
    if (next >= 0) {
      e.preventDefault();
      buttons[next]?.focus();
    }
    // Keys inside the menu are not the grid's.
    e.stopPropagation();
  };

  return (
    <>
      <button
        type="button"
        className="menu-button"
        popoverTarget={id}
        aria-haspopup="menu"
        aria-label={label}
        data-menu-button=""
        tabIndex={-1}
      >
        <span aria-hidden="true">⋯</span>
      </button>
      <div id={id} ref={panel} popover="auto" role="menu" aria-label={label} className="menu" onToggle={onToggle} onKeyDown={onKeyDown}>
        {items.map((item) => (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            className={item.danger ? "menu-item menu-item-danger" : "menu-item"}
            disabled={item.disabled}
            onClick={() => {
              panel.current?.hidePopover();
              item.onSelect();
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
    </>
  );
}
