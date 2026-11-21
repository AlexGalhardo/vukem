import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import type { Column } from "../api/types";
import { SortIcon } from "../components/icons";
import { CellContent, PlaceholderCell } from "./Cell";
import { columnKind } from "./filters";
import type { ViewState } from "./filters";
import { columnWidth, formatInt } from "./format";
import { columnOffsets, computeWindow, rowHeights, rowTop } from "./virtual";
import type { Density } from "./virtual";
import type { GridData } from "./useGridData";

export const headerHeight = 36;
export const gutterWidth = 56;

export interface GridProps {
  label: string;
  columns: readonly Column[];
  primaryKey: readonly string[];
  data: GridData;
  view: ViewState;
  onSort: (column: string) => void;
  density: Density;
}

interface Size {
  width: number;
  height: number;
}

/**
 * Grid is the virtualized, read-only data grid: it draws only the rows and
 * columns in view (plus a little overscan), places them with transforms set
 * through the CSSOM (which the strict CSP allows), and keeps the header and
 * the first columns in view with position: sticky.
 */
export function Grid({ label, columns, primaryKey, data, view, onSort, density }: GridProps): ReactElement {
  const scroller = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState({ top: 0, left: 0 });
  const [size, setSize] = useState<Size>({ width: 0, height: 0 });
  const frame = useRef(0);

  const rowHeight = rowHeights[density];
  const kinds = useMemo(() => columns.map((c) => columnKind(c.type)), [columns]);
  const widths = useMemo(() => columns.map(columnWidth), [columns]);
  const offsets = useMemo(() => columnOffsets(widths), [widths]);

  const viewportHeight = Math.max(size.height - headerHeight, 0);
  const win = computeWindow({
    scrollTop: scroll.top,
    scrollLeft: scroll.left,
    viewportHeight,
    viewportWidth: Math.max(size.width - gutterWidth, 0),
    rowCount: data.rowCount,
    rowHeight,
    columnWidths: widths,
  });

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) {
      return;
    }
    const measure = (): void => setSize({ width: el.clientWidth, height: el.clientHeight });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  const onScroll = useCallback(() => {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const el = scroller.current;
      if (el) {
        setScroll({ top: el.scrollTop, left: el.scrollLeft });
      }
    });
  }, []);

  const { want } = data;
  useEffect(() => {
    if (win.rows.end > win.rows.start) {
      want(win.rows.start, win.rows.end);
    }
  }, [want, win.rows.start, win.rows.end]);

  // The columns to draw: the first one is pinned next to the gutter, the rest follow the window.
  const first = Math.max(win.columns.start, 1);
  const spacer = (offsets[first] ?? 0) - (widths[0] ?? 0);
  const pinned = columns[0];

  const realTop = scroll.top * win.scale;
  const firstVisible = Math.min(Math.floor(realTop / rowHeight) + 1, data.rowCount);
  const lastVisible = Math.min(Math.ceil((realTop + viewportHeight) / rowHeight), data.rowCount);
  const canvasWidth = gutterWidth + win.totalWidth;
  const rows: ReactElement[] = [];

  for (let i = win.rows.start; i < win.rows.end; i++) {
    const cells = data.row(i);
    const top = rowTop(i, rowHeight, scroll.top, win.scale);

    rows.push(
      <div
        key={i}
        role="row"
        aria-rowindex={i + 2}
        aria-busy={cells ? undefined : true}
        className={i % 2 === 1 ? "grid-row grid-row-odd" : "grid-row"}
        style={{ transform: `translateY(${top}px)`, height: rowHeight, width: canvasWidth }}
      >
        <div
          role="gridcell"
          aria-colindex={1}
          className="grid-cell grid-gutter"
          style={{ width: gutterWidth }}
        >
          <span className="row-number">{i + 1}</span>
        </div>
        {pinned ? (
          <div
            role="gridcell"
            aria-colindex={2}
            className={`grid-cell grid-pinned kind-${kinds[0] ?? "text"}`}
            style={{ width: widths[0], left: gutterWidth }}
          >
            {cells?.[0] ? <CellContent cell={cells[0]} kind={kinds[0] ?? "text"} /> : <PlaceholderCell />}
          </div>
        ) : null}
        {spacer > 0 ? <div className="grid-spacer" aria-hidden="true" style={{ width: spacer }} /> : null}
        {columns.slice(first, win.columns.end).map((c, k) => {
          const index = first + k;
          const cell = cells?.[index];
          return (
            <div
              key={c.name}
              role="gridcell"
              aria-colindex={index + 2}
              className={`grid-cell kind-${kinds[index] ?? "text"}`}
              style={{ width: widths[index] }}
            >
              {cell ? <CellContent cell={cell} kind={kinds[index] ?? "text"} /> : <PlaceholderCell />}
            </div>
          );
        })}
      </div>,
    );
  }

  const header = (index: number, c: Column): ReactElement => {
    const sorted = view.sort === c.name;
    return (
      <div
        key={c.name}
        role="columnheader"
        aria-colindex={index + 2}
        aria-sort={sorted ? (view.desc ? "descending" : "ascending") : undefined}
        className={index === 0 ? "grid-cell grid-hcell grid-pinned" : "grid-cell grid-hcell"}
        style={index === 0 ? { width: widths[0], left: gutterWidth } : { width: widths[index] }}
      >
        <button type="button" className="hcell-button" onClick={() => onSort(c.name)}>
          <span className="hcell-name truncate">{c.name}</span>
          {primaryKey.includes(c.name) ? <span className="visually-hidden">primary key</span> : null}
          {primaryKey.includes(c.name) ? (
            <span className="hcell-pk" aria-hidden="true">
              PK
            </span>
          ) : null}
          <span className="hcell-type truncate" aria-hidden="true">
            {c.type}
          </span>
          {sorted ? <SortIcon className="hcell-sort" direction={view.desc ? "desc" : "asc"} /> : null}
        </button>
      </div>
    );
  };

  return (
    <div className="grid-wrap">
      <div
        ref={scroller}
        className="grid-scroll"
        role="grid"
        aria-label={label}
        aria-rowcount={data.rowCount + 1}
        aria-colcount={columns.length + 1}
        onScroll={onScroll}
      >
        <div className="grid-canvas" style={{ width: canvasWidth, height: headerHeight + win.totalHeight }}>
          <div role="row" aria-rowindex={1} className="grid-header" style={{ width: canvasWidth, height: headerHeight }}>
            <div
              role="columnheader"
              aria-colindex={1}
              className="grid-cell grid-hcell grid-gutter"
              style={{ width: gutterWidth }}
            >
              <span className="visually-hidden">Row</span>
            </div>
            {columns[0] ? header(0, columns[0]) : null}
            {spacer > 0 ? <div className="grid-spacer" aria-hidden="true" style={{ width: spacer }} /> : null}
            {columns.slice(first, win.columns.end).map((c, k) => header(first + k, c))}
          </div>
          <div className="grid-body" style={{ height: win.totalHeight }}>
            {rows}
          </div>
        </div>
      </div>
      <div className="grid-footer">
        <span data-testid="grid-position">
          {data.rowCount === 0
            ? data.loading
              ? "Loading rows…"
              : "No rows"
            : `Rows ${formatInt(firstVisible)} to ${formatInt(lastVisible)} of ${data.approx ? "about " : ""}${formatInt(data.rowCount)}`}
        </span>
        {data.approx ? (
          <button
            type="button"
            className="button button-small"
            onClick={data.askCount}
            disabled={data.countState === "loading"}
          >
            {data.countState === "loading" ? "Counting…" : "Exact count"}
          </button>
        ) : null}
        {data.countState === "unavailable" ? (
          <span className="muted" role="status">
            The exact count is not available: counting took too long.
          </span>
        ) : null}
        {data.capped ? (
          <span className="muted">Sorted views show the first {formatInt(data.rowCount)} rows.</span>
        ) : null}
      </div>
    </div>
  );
}
