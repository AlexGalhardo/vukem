import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { DragEvent, KeyboardEvent, MouseEvent, ReactElement } from "react";
import { CloseIcon, TableIcon } from "../components/icons";
import { isUnsaved } from "./tabsState";
import type { Tab } from "./tabsState";

export function tabDomId(prefix: string, id: string): string {
  return `${prefix}-tab-${id}`;
}

export function panelDomId(prefix: string, id: string): string {
  return `${prefix}-panel-${id}`;
}

export interface TabStripProps {
  /** Makes the DOM ids of this strip unique on the page. */
  idPrefix: string;
  tabs: readonly Tab[];
  active: string | null;
  onActivate: (id: string) => void;
  /** Asks to close: the owner may ask for confirmation first. */
  onClose: (id: string) => void;
  onShift: (id: string, delta: 1 | -1) => void;
  onMove: (id: string, to: number) => void;
}

/**
 * TabStrip follows the ARIA tabs pattern: a tablist whose tabs are buttons with
 * roving tabindex (only the selected one is a tab stop), Arrow keys, Home and
 * End to move, Delete to close and Alt+Arrow to reorder. Selection follows
 * focus. The close buttons are for the mouse and stay out of the tab order,
 * since Delete does the same from the keyboard.
 */
export function TabStrip({ idPrefix, tabs, active, onActivate, onClose, onShift, onMove }: TabStripProps): ReactElement {
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const focusAfter = useRef<"active" | string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);

  // Focus follows a keyboard action that moved, removed or re-created the tab buttons.
  useLayoutEffect(() => {
    const want = focusAfter.current;
    if (want === null) {
      return;
    }
    focusAfter.current = null;
    const id = want === "active" ? active : want;
    if (id !== null) {
      buttons.current.get(id)?.focus();
    }
  });

  useEffect(() => {
    // A tab that scrolled out of the strip comes back into view when it is selected.
    if (active !== null) {
      buttons.current.get(active)?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
    }
  }, [active]);

  const go = (index: number): void => {
    const tab = tabs[(index + tabs.length) % tabs.length];
    if (tab) {
      focusAfter.current = tab.id;
      onActivate(tab.id);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLButtonElement>, tab: Tab, index: number): void => {
    switch (e.key) {
      case "ArrowRight":
      case "ArrowLeft": {
        e.preventDefault();
        const delta = e.key === "ArrowRight" ? 1 : -1;
        if (e.altKey) {
          focusAfter.current = tab.id;
          onShift(tab.id, delta);
        } else {
          go(index + delta);
        }
        break;
      }
      case "Home":
        e.preventDefault();
        go(0);
        break;
      case "End":
        e.preventDefault();
        go(tabs.length - 1);
        break;
      case "Delete":
        e.preventDefault();
        focusAfter.current = "active";
        onClose(tab.id);
        break;
    }
  };

  const onDrop = (e: DragEvent, to: number): void => {
    e.preventDefault();
    if (dragging !== null) {
      onMove(dragging, to);
    }
    setDragging(null);
  };

  return (
    <div className="tabstrip" role="tablist" aria-label="Open tabs" aria-orientation="horizontal">
      {tabs.map((tab, index) => {
        const selected = tab.id === active;
        const unsaved = isUnsaved(tab);
        return (
          <div
            key={tab.id}
            role="presentation"
            className={selected ? "tab tab-selected" : "tab"}
            draggable
            onDragStart={(e) => {
              setDragging(tab.id);
              e.dataTransfer.effectAllowed = "move";
              e.dataTransfer.setData("text/plain", tab.id);
            }}
            onDragOver={(e) => {
              if (dragging !== null) {
                e.preventDefault();
              }
            }}
            onDrop={(e) => onDrop(e, index)}
            onDragEnd={() => setDragging(null)}
          >
            <button
              type="button"
              role="tab"
              id={tabDomId(idPrefix, tab.id)}
              ref={(el) => {
                if (el) {
                  buttons.current.set(tab.id, el);
                } else {
                  buttons.current.delete(tab.id);
                }
              }}
              className="tab-button"
              aria-selected={selected}
              aria-controls={panelDomId(idPrefix, tab.id)}
              tabIndex={selected ? 0 : -1}
              onClick={() => onActivate(tab.id)}
              onAuxClick={(e: MouseEvent) => {
                if (e.button === 1) {
                  e.preventDefault();
                  onClose(tab.id);
                }
              }}
              onMouseDown={(e) => {
                // Middle click would start autoscroll before auxclick arrives.
                if (e.button === 1) {
                  e.preventDefault();
                }
              }}
              onKeyDown={(e) => onKeyDown(e, tab, index)}
            >
              {tab.kind === "table" ? <TableIcon className="tab-icon" /> : null}
              <span className="truncate tab-title">{tab.title}</span>
              {tab.pinned ? <span className="visually-hidden"> (pinned)</span> : null}
              {unsaved ? (
                <>
                  <span className="tab-dot" aria-hidden="true" />
                  <span className="visually-hidden"> (unsaved changes)</span>
                </>
              ) : null}
            </button>
            <button
              type="button"
              className="tab-close"
              tabIndex={-1}
              aria-label={`Close ${tab.title}`}
              onClick={() => onClose(tab.id)}
            >
              <CloseIcon />
            </button>
          </div>
        );
      })}
    </div>
  );
}
