import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, PointerEvent, ReactElement } from "react";
import { neighboursOf } from "./layout";
import type { Layout } from "./layout";
import { fit, keyAction, zoomBy, zoomStep } from "./viewport";
import type { Viewport } from "./viewport";

export interface DiagramProps {
  layout: Layout;
  /** A line under each table's name, such as its column count. */
  subtitles: ReadonlyMap<string, string>;
  selected: string | null;
  onSelect: (name: string) => void;
  /** Enter, Space or a double click on a table. */
  onOpen: (name: string) => void;
  description: string;
}

const maxLabel = 24;

function shorten(name: string): string {
  return name.length > maxLabel ? `${name.slice(0, maxLabel - 1)}…` : name;
}

/**
 * Diagram draws the relationship layout as SVG. It can be panned (drag or the
 * arrow keys) and zoomed (buttons, + and -, never the mouse wheel, so the page
 * still scrolls). Every table is a focusable link.
 */
export function Diagram({ layout, subtitles, selected, onSelect, onOpen, description }: DiagramProps): ReactElement {
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [view, setView] = useState<Viewport>({ x: 0, y: 0, scale: 1 });
  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null);
  const fitted = useRef<Layout | null>(null);
  const descId = useId();

  useLayoutEffect(() => {
    const el = box.current;
    if (!el) {
      return;
    }
    const measure = (): void => setSize({ width: el.clientWidth, height: el.clientHeight });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // A new drawing starts fitted to the box.
  useEffect(() => {
    if (size.width > 0 && fitted.current !== layout) {
      fitted.current = layout;
      setView(fit(layout.width, layout.height, size.width, size.height));
    }
  }, [layout, size.width, size.height]);

  const near = useMemo(() => (selected === null ? new Set<string>() : neighboursOf(layout, selected)), [layout, selected]);

  const zoom = useCallback(
    (factor: number): void => setView((v) => zoomBy(v, factor, size.width, size.height)),
    [size.width, size.height],
  );

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    if (e.ctrlKey || e.metaKey || e.altKey) {
      return;
    }
    const next = keyAction(e.key, view, size.width, size.height, layout.width, layout.height);
    if (next) {
      e.preventDefault();
      setView(next);
    }
  };

  const onPointerDown = (e: PointerEvent<HTMLDivElement>): void => {
    if (e.button !== 0 || (e.target as Element).closest("[data-node]")) {
      return;
    }
    drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  };
  const onPointerMove = (e: PointerEvent<HTMLDivElement>): void => {
    const d = drag.current;
    if (d) {
      setView((v) => ({ ...v, x: d.vx + e.clientX - d.x, y: d.vy + e.clientY - d.y }));
    }
  };
  const onPointerUp = (): void => {
    drag.current = null;
  };

  const nodeKey = (e: KeyboardEvent<SVGGElement>, name: string): void => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      e.stopPropagation();
      onOpen(name);
    }
  };

  return (
    <div className="diagram">
      <div className="diagram-tools" role="toolbar" aria-label="Diagram view">
        <button type="button" className="button button-small" onClick={() => zoom(zoomStep)} aria-label="Zoom in">
          +
        </button>
        <button type="button" className="button button-small" onClick={() => zoom(1 / zoomStep)} aria-label="Zoom out">
          -
        </button>
        <button
          type="button"
          className="button button-small"
          onClick={() => setView(fit(layout.width, layout.height, size.width, size.height))}
        >
          Fit to view
        </button>
        <span className="muted diagram-hint">Drag or use the arrow keys to move, + and - to zoom, 0 to fit.</span>
      </div>
      <p id={descId} className="visually-hidden">
        {description}
      </p>
      <div
        ref={box}
        className="diagram-box"
        tabIndex={0}
        role="group"
        aria-label="Relationship diagram"
        aria-describedby={descId}
        data-testid="diagram"
        onKeyDown={onKeyDown}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        <svg className="diagram-svg" width={size.width} height={size.height}>
          <defs>
            <marker id="diagram-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto">
              <path d="M 0 0 L 10 5 L 0 10 z" className="diagram-arrow" />
            </marker>
          </defs>
          <g
            data-testid="diagram-viewport"
            data-scale={view.scale.toFixed(3)}
            data-x={Math.round(view.x)}
            data-y={Math.round(view.y)}
            transform={`translate(${view.x} ${view.y}) scale(${view.scale})`}
          >
            {layout.edges.map((edge, i) => {
              const active = selected !== null && (edge.from === selected || edge.to === selected);
              return (
                <polyline
                  key={`${edge.from}>${edge.to}#${i}`}
                  className={active ? "diagram-edge diagram-edge-active" : "diagram-edge"}
                  points={edge.points.map((p) => `${p.x},${p.y}`).join(" ")}
                  markerEnd="url(#diagram-arrow)"
                />
              );
            })}
            {layout.nodes.map((n) => {
              const state = n.name === selected ? " diagram-node-selected" : near.has(n.name) ? " diagram-node-near" : selected !== null ? " diagram-node-dim" : "";
              return (
                <g
                  key={n.name}
                  data-node={n.name}
                  className={`diagram-node${state}`}
                  transform={`translate(${n.x} ${n.y})`}
                  tabIndex={0}
                  role="link"
                  aria-label={`${n.name}, ${subtitles.get(n.name) ?? ""}. Press Enter to open it.`}
                  onFocus={() => onSelect(n.name)}
                  onClick={() => onSelect(n.name)}
                  onDoubleClick={() => onOpen(n.name)}
                  onKeyDown={(e) => nodeKey(e, n.name)}
                >
                  <title>{n.name}</title>
                  <rect width={n.width} height={n.height} rx={6} className="diagram-rect" />
                  <text x={10} y={19} className="diagram-name">
                    {shorten(n.name)}
                  </text>
                  <text x={10} y={36} className="diagram-sub">
                    {subtitles.get(n.name) ?? ""}
                  </text>
                </g>
              );
            })}
          </g>
        </svg>
      </div>
    </div>
  );
}
