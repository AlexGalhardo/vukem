import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, MouseEvent, ReactElement } from "react";
import type { ApiCell, Column } from "../api/types";
import { LinkIcon, SortIcon } from "../components/icons";
import { Link } from "../lib/history";
import type { RowLink } from "./links";
import { CellContent, PlaceholderCell } from "./Cell";
import { copyText } from "./clipboard";
import { columnKind } from "./filters";
import type { ColumnKind, ViewState } from "./filters";
import { columnWidth, formatInt, plainText, toCSV, toJSON, toTSV } from "./format";
import { clampPos, keyAction, selectRange, shouldAnnounce } from "./keyboard";
import type { Pos } from "./keyboard";
import { Menu } from "./Menu";
import type { MenuItem } from "./Menu";
import type { GridData } from "./useGridData";
import {
  columnOffsets,
  computeWindow,
  rowHeights,
  rowTop,
  scrollLeftToShowColumn,
  scrollTopToShowRow,
} from "./virtual";
import type { Density } from "./virtual";

export const headerHeight = 36;
export const gutterWidth = 96;

export interface GridProps {
  label: string;
  columns: readonly Column[];
  primaryKey: readonly string[];
  data: GridData;
  view: ViewState;
  /** The header click: ascending, then descending, then none. */
  onSort: (column: string) => void;
  /** The column menu: sort to exactly this. */
  onSetSort: (column: string | null, desc: boolean) => void;
  density: Density;
  /** Says something to a screen reader, politely. */
  announce: (text: string) => void;
  /** Changes with the view of the table: the selection and focus start over. */
  resetKey: string;
  onOpenRow: (index: number) => void;
  onFilterColumn?: (column: string) => void;
  /** The link a cell draws for a foreign key, if its column has one. */
  linkFor?: (column: string, cells: readonly ApiCell[]) => RowLink | null;
  source: string;
}

interface Size {
  width: number;
  height: number;
}

function inEditable(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest("input, textarea, select, [contenteditable], [popover], dialog") !== null;
}

/**
 * Grid is the virtualized data grid: it draws only the rows and columns in
 * view (plus a little overscan), places them with transforms set through the
 * CSSOM (which the strict CSP allows), and keeps the header and the first
 * columns in view with position: sticky. It is an ARIA grid with one tab stop
 * (the active cell moves with the arrow keys) and real row and column counts.
 */
export function Grid({
  label,
  columns,
  primaryKey,
  data,
  view,
  onSort,
  onSetSort,
  density,
  announce,
  resetKey,
  onOpenRow,
  onFilterColumn,
  linkFor,
  source,
}: GridProps): ReactElement {
  const scroller = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState({ top: 0, left: 0 });
  const [size, setSize] = useState<Size>({ width: 0, height: 0 });
  const [focus, setFocus] = useState<Pos>({ row: 0, col: 1 });
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set());
  const [anchor, setAnchor] = useState(0);
  const [manualCopy, setManualCopy] = useState<string | null>(null);
  const frame = useRef(0);
  const hasFocus = useRef(false);
  const pendingShow = useRef<Pos | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);

  const rowHeight = rowHeights[density];
  const kinds = useMemo(() => columns.map((c) => columnKind(c.type)), [columns]);
  const widths = useMemo(() => columns.map(columnWidth), [columns]);
  const offsets = useMemo(() => columnOffsets(widths), [widths]);
  const cols = columns.length + 1;

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

  const pageRows = Math.max(Math.floor(viewportHeight / rowHeight) - 1, 1);
  const dims = { rows: data.rowCount, cols, page: pageRows };
  const active = data.rowCount === 0 && focus.row >= 0 ? { row: -1, col: focus.col } : clampPos(focus, dims);

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

  useEffect(() => {
    setSelected(new Set());
    setFocus({ row: 0, col: 1 });
    scroller.current?.scrollTo({ top: 0, left: 0 });
  }, [resetKey]);

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

  // The focused cell is never allowed to leave the page: if the window moved on, it is still drawn.
  useEffect(() => {
    if (!hasFocus.current) {
      return;
    }
    if (inEditable(document.activeElement)) {
      return;
    }
    const el = scroller.current?.querySelector<HTMLElement>(`[data-r="${active.row}"][data-c="${active.col}"]`);
    if (el && document.activeElement !== el) {
      el.focus({ preventScroll: true });
    }
  });

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (manualCopy !== null && !d.open) {
      d.showModal();
      d.querySelector("textarea")?.select();
    }
  }, [manualCopy]);

  const showCell = useCallback(
    (pos: Pos): void => {
      const el = scroller.current;
      if (!el) {
        return;
      }
      if (pos.row >= 0) {
        el.scrollTop = scrollTopToShowRow(pos.row, rowHeight, el.scrollTop, el.clientHeight - headerHeight, data.rowCount);
      }
      if (pos.col >= 2) {
        el.scrollLeft = scrollLeftToShowColumn(pos.col - 1, offsets, el.scrollLeft, el.clientWidth - gutterWidth, widths[0] ?? 0);
      } else if (pos.col <= 1) {
        el.scrollLeft = 0;
      }
    },
    [rowHeight, data.rowCount, offsets, widths],
  );

  // After a jump to the end the row count and the scroll size change; scroll once they have.
  useEffect(() => {
    if (pendingShow.current) {
      showCell(pendingShow.current);
      pendingShow.current = null;
    }
  }, [data.rowCount, data.tail, showCell]);

  const rowsOf = useCallback(
    (indexes: readonly number[]): ApiCell[][] =>
      indexes.map((i) => data.row(i)).filter((r): r is ApiCell[] => r !== undefined),
    [data],
  );

  const copy = useCallback(
    async (text: string, what: string): Promise<void> => {
      if (await copyText(text)) {
        announce(`Copied ${what}.`);
      } else {
        setManualCopy(text);
        announce("Copying was blocked. The text is in a dialog to select.");
      }
    },
    [announce],
  );

  const selectedRows = useMemo(() => [...selected].sort((a, b) => a - b), [selected]);

  const copyRows = useCallback(
    (indexes: readonly number[], format: "tsv" | "csv" | "json"): void => {
      const rows = rowsOf(indexes);
      if (rows.length === 0) {
        announce("Those rows are not loaded yet.");
        return;
      }
      const text = format === "csv" ? toCSV(columns, rows) : format === "json" ? toJSON(columns, rows) : toTSV(columns, rows);
      void copy(text, `${rows.length} row${rows.length === 1 ? "" : "s"} as ${format.toUpperCase()}`);
    },
    [rowsOf, columns, copy, announce],
  );

  const copyColumn = useCallback(
    (index: number, format: "csv" | "json"): void => {
      const column = columns[index];
      if (!column) {
        return;
      }
      const rows: ApiCell[][] = [];
      for (let i = 0; i < data.rowCount; i++) {
        const r = data.row(i);
        if (r?.[index]) {
          rows.push([r[index]]);
        }
      }
      const text = format === "csv" ? toCSV([column], rows) : toJSON([column], rows);
      void copy(text, `${rows.length} loaded values of ${column.name} as ${format.toUpperCase()}`);
    },
    [columns, data, copy],
  );

  const copyActive = useCallback((): void => {
    if (selectedRows.length > 0) {
      copyRows(selectedRows, "tsv");
      return;
    }
    const cell = active.row >= 0 && active.col >= 1 ? data.row(active.row)?.[active.col - 1] : undefined;
    if (cell) {
      void copy(plainText(cell), "the cell");
    }
  }, [selectedRows, copyRows, active.row, active.col, data, copy]);

  const move = useCallback(
    (to: Pos, extend: boolean): void => {
      if (!extend && shouldAnnounce(Math.max(active.row, 0), Math.max(to.row, 0)) && to.row >= 0) {
        announce(`Row ${formatInt(to.row + 1)} of ${data.approx ? "about " : ""}${formatInt(data.rowCount)}`);
      }
      if (extend) {
        const next = new Set(selected);
        for (const i of selectRange(anchor, to.row)) {
          next.add(i);
        }
        setSelected(next);
        announce(`${next.size} rows selected`);
      }
      setFocus(to);
      showCell(to);
    },
    [active.row, anchor, announce, data.approx, data.rowCount, selected, showCell],
  );

  const jumpToLastRow = useCallback(
    (col: number): void => {
      void data.jumpToEnd().then((rows) => {
        if (rows === null) {
          announce("The last row cannot be reached: the exact count is not available.");
          return;
        }
        const to = { row: Math.max(rows - 1, 0), col: Math.max(col, 1) };
        announce(`Row ${formatInt(to.row + 1)} of ${formatInt(rows)}`);
        setFocus(to);
        pendingShow.current = to;
      });
    },
    [data, announce],
  );

  const toggleRow = useCallback(
    (index: number): void => {
      const next = new Set(selected);
      if (next.has(index)) {
        next.delete(index);
      } else {
        next.add(index);
      }
      setSelected(next);
      setAnchor(index);
      announce(`${next.size} row${next.size === 1 ? "" : "s"} selected`);
    },
    [selected, announce],
  );

  const activate = useCallback((): void => {
    if (active.row < 0) {
      if (active.col >= 1) {
        const column = columns[active.col - 1];
        if (column) {
          onSort(column.name);
        }
      }
      return;
    }
    onOpenRow(active.row);
  }, [active.row, active.col, columns, onSort, onOpenRow]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    if (inEditable(e.target)) {
      return;
    }
    const action = keyAction(active, dims, { key: e.key, ctrl: e.ctrlKey, meta: e.metaKey, shift: e.shiftKey, alt: e.altKey });
    if (action.type === "none") {
      return;
    }
    // Escape with nothing to clear is left to the browser (it closes a menu).
    if (action.type !== "cancel" || selected.size > 0) {
      e.preventDefault();
    }

    switch (action.type) {
      case "move":
        if (action.to.row === 0 && action.to.col === 0 && e.ctrlKey && data.tail) {
          data.leaveTail();
        }
        if (action.extend && !selected.has(active.row)) {
          setAnchor(active.row);
        }
        move(action.to, action.extend);
        break;
      case "jumpEnd":
        jumpToLastRow(action.to.col);
        break;
      case "activate":
        activate();
        break;
      case "toggleSelect":
        if (active.row >= 0) {
          toggleRow(active.row);
        } else {
          activate();
        }
        break;
      case "copy":
        copyActive();
        break;
      case "cancel":
        if (selected.size > 0) {
          setSelected(new Set());
          announce("Selection cleared");
        }
        break;
      case "menu": {
        const cell = scroller.current?.querySelector(`[data-r="${active.row}"][data-c="${active.col}"]`);
        const row = active.row >= 0 ? cell?.parentElement : cell;
        const button = (active.row >= 0 ? row : cell)?.querySelector<HTMLButtonElement>("[data-menu-button]");
        button?.click();
        break;
      }
    }
  };

  const onClick = (e: MouseEvent<HTMLDivElement>): void => {
    const cell = e.target instanceof Element ? e.target.closest<HTMLElement>("[data-r]") : null;
    if (!cell) {
      return;
    }
    setFocus({ row: Number(cell.dataset.r), col: Number(cell.dataset.c) });
  };

  const refocus = useCallback(() => {
    hasFocus.current = true;
  }, []);

  // The columns to draw: the first one is pinned next to the gutter, the rest follow the window.
  const first = Math.max(win.columns.start, 1);
  const spacer = (offsets[first] ?? 0) - (widths[0] ?? 0);
  const pinned = columns[0];
  const outside = (index: number): boolean => index >= 1 && (index < first || index >= win.columns.end);

  const realTop = scroll.top * win.scale;
  const firstVisible = Math.min(Math.floor(realTop / rowHeight) + 1, data.rowCount);
  const lastVisible = Math.min(Math.ceil((realTop + viewportHeight) / rowHeight), data.rowCount);
  const canvasWidth = gutterWidth + win.totalWidth;

  const dataCell = (rowIndex: number, index: number, cells: ApiCell[] | undefined, extra: boolean): ReactElement => {
    const kind: ColumnKind = kinds[index] ?? "text";
    const c = columns[index];
    const cell = cells?.[index];
    const isActive = active.row === rowIndex && active.col === index + 1;
    const pinnedCell = index === 0;
    const style = pinnedCell
      ? { width: widths[0], left: gutterWidth }
      : extra
        ? { width: widths[index], position: "absolute" as const, left: gutterWidth + (offsets[index] ?? 0) }
        : { width: widths[index] };

    return (
      <div
        key={c?.name ?? index}
        role="gridcell"
        aria-colindex={index + 2}
        data-r={rowIndex}
        data-c={index + 1}
        data-active={isActive ? "true" : undefined}
        tabIndex={isActive ? 0 : -1}
        className={`grid-cell${pinnedCell ? " grid-pinned" : ""} kind-${kind}`}
        style={style}
      >
        {cell ? (
          <CellContent
            cell={cell}
            kind={kind}
            onExpand={() => onOpenRow(rowIndex)}
            link={fkLink(c?.name, cells)}
          />
        ) : (
          <PlaceholderCell />
        )}
      </div>
    );
  };

  const fkLink = (column: string | undefined, cells: ApiCell[] | undefined): ReactElement | null => {
    const link = column && cells ? linkFor?.(column, cells) : null;
    return link ? (
      <Link
        route={{ name: "table", source, table: link.table }}
        search={link.search}
        className="cell-link"
        tabIndex={-1}
        ariaLabel={link.label}
      >
        <LinkIcon />
      </Link>
    ) : null;
  };

  const rowIndexes: number[] = [];
  for (let i = win.rows.start; i < win.rows.end; i++) {
    rowIndexes.push(i);
  }
  if (active.row >= 0 && (active.row < win.rows.start || active.row >= win.rows.end)) {
    rowIndexes.push(active.row);
  }

  const rows = rowIndexes.map((i) => {
    const cells = data.row(i);
    const top = rowTop(i, rowHeight, scroll.top, win.scale);
    const isSelected = selected.has(i);
    const extra = active.row === i && outside(active.col - 1) ? active.col - 1 : -1;

    return (
      <div
        key={i}
        role="row"
        aria-rowindex={i + 2}
        aria-selected={isSelected}
        aria-busy={cells ? undefined : true}
        className={i % 2 === 1 ? "grid-row grid-row-odd" : "grid-row"}
        style={{ transform: `translateY(${top}px)`, height: rowHeight, width: canvasWidth }}
      >
        <div
          role="gridcell"
          aria-colindex={1}
          data-r={i}
          data-c={0}
          data-active={active.row === i && active.col === 0 ? "true" : undefined}
          tabIndex={active.row === i && active.col === 0 ? 0 : -1}
          className="grid-cell grid-gutter"
          style={{ width: gutterWidth }}
        >
          <input
            type="checkbox"
            tabIndex={-1}
            className="row-check"
            aria-label={`Select row ${formatInt(i + 1)}`}
            checked={isSelected}
            onChange={() => toggleRow(i)}
            onClick={(e) => {
              if (e.shiftKey) {
                const next = new Set(selected);
                for (const k of selectRange(anchor, i)) {
                  next.add(k);
                }
                setSelected(next);
                e.preventDefault();
              }
            }}
          />
          <span className="row-number">{formatInt(i + 1)}</span>
          <Menu
            id={`row-menu-${i}`}
            label={`Actions for row ${formatInt(i + 1)}`}
            onClose={refocus}
            items={rowMenuItems(i)}
          />
        </div>
        {pinned ? dataCell(i, 0, cells, false) : null}
        {spacer > 0 ? <div className="grid-spacer" aria-hidden="true" style={{ width: spacer }} /> : null}
        {columns.slice(first, win.columns.end).map((_, k) => dataCell(i, first + k, cells, false))}
        {extra > 0 ? dataCell(i, extra, cells, true) : null}
      </div>
    );
  });

  function rowMenuItems(i: number): MenuItem[] {
    const targets = selected.has(i) && selected.size > 1 ? selectedRows : [i];
    return [
      { label: "Open row", onSelect: () => onOpenRow(i) },
      { label: targets.length > 1 ? "Copy selected as TSV" : "Copy row as TSV", onSelect: () => copyRows(targets, "tsv") },
      { label: targets.length > 1 ? "Copy selected as JSON" : "Copy row as JSON", onSelect: () => copyRows(targets, "json") },
      { label: targets.length > 1 ? "Copy selected as CSV" : "Copy row as CSV", onSelect: () => copyRows(targets, "csv") },
    ];
  }

  const columnMenuItems = (index: number, c: Column): MenuItem[] => {
    const items: MenuItem[] = [
      { label: "Sort ascending", onSelect: () => onSetSort(c.name, false) },
      { label: "Sort descending", onSelect: () => onSetSort(c.name, true) },
      { label: "Clear sort", onSelect: () => onSetSort(null, false), disabled: view.sort !== c.name },
    ];
    if (onFilterColumn) {
      items.push({ label: "Filter by this column", onSelect: () => onFilterColumn(c.name) });
    }
    items.push(
      { label: "Copy loaded values as JSON", onSelect: () => copyColumn(index, "json") },
      { label: "Copy loaded values as CSV", onSelect: () => copyColumn(index, "csv") },
    );
    return items;
  };

  const header = (index: number, c: Column, extra: boolean): ReactElement => {
    const sorted = view.sort === c.name;
    const isActive = active.row === -1 && active.col === index + 1;
    const pinnedCell = index === 0;
    const style = pinnedCell
      ? { width: widths[0], left: gutterWidth }
      : extra
        ? { width: widths[index], position: "absolute" as const, left: gutterWidth + (offsets[index] ?? 0) }
        : { width: widths[index] };

    return (
      <div
        key={c.name}
        role="columnheader"
        aria-colindex={index + 2}
        aria-sort={sorted ? (view.desc ? "descending" : "ascending") : undefined}
        data-r={-1}
        data-c={index + 1}
        data-active={isActive ? "true" : undefined}
        tabIndex={isActive ? 0 : -1}
        className={pinnedCell ? "grid-cell grid-hcell grid-pinned" : "grid-cell grid-hcell"}
        style={style}
      >
        <button type="button" tabIndex={-1} className="hcell-button" onClick={() => onSort(c.name)}>
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
        <Menu id={`col-menu-${index}`} label={`Column menu for ${c.name}`} onClose={refocus} items={columnMenuItems(index, c)} />
      </div>
    );
  };

  const headerExtra = active.row === -1 && outside(active.col - 1) ? active.col - 1 : -1;
  const activeCell = active.row >= 0 && active.col >= 1 ? data.row(active.row)?.[active.col - 1] : undefined;

  return (
    <div className="grid-wrap">
      <div className="grid-actions">
        <button
          type="button"
          className="button button-small"
          onClick={() => onOpenRow(Math.max(active.row, 0))}
          disabled={data.rowCount === 0}
        >
          Open row
        </button>
        <button type="button" className="button button-small" onClick={copyActive} disabled={!activeCell && selected.size === 0}>
          {selected.size > 0 ? `Copy ${selected.size} selected` : "Copy cell"}
        </button>
        {selected.size > 0 ? (
          <button
            type="button"
            className="button button-small"
            onClick={() => {
              setSelected(new Set());
              announce("Selection cleared");
            }}
          >
            Clear selection
          </button>
        ) : null}
        <button
          type="button"
          className="button button-small"
          onClick={() => jumpToLastRow(active.col)}
          disabled={data.rowCount === 0}
        >
          Last row
        </button>
        {data.tail ? (
          <button
            type="button"
            className="button button-small"
            onClick={() => {
              data.leaveTail();
              move({ row: 0, col: active.col || 1 }, false);
            }}
          >
            First row
          </button>
        ) : null}
      </div>
      <div
        ref={scroller}
        className="grid-scroll"
        role="grid"
        aria-label={label}
        aria-rowcount={data.rowCount + 1}
        aria-colcount={cols}
        aria-multiselectable="true"
        onScroll={onScroll}
        onKeyDown={onKeyDown}
        onClick={onClick}
        onFocus={() => {
          hasFocus.current = true;
        }}
        onBlur={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
            hasFocus.current = false;
          }
        }}
      >
        <div className="grid-canvas" style={{ width: canvasWidth, height: headerHeight + win.totalHeight }}>
          <div role="row" aria-rowindex={1} className="grid-header" style={{ width: canvasWidth, height: headerHeight }}>
            <div
              role="columnheader"
              aria-colindex={1}
              data-r={-1}
              data-c={0}
              data-active={active.row === -1 && active.col === 0 ? "true" : undefined}
              tabIndex={active.row === -1 && active.col === 0 ? 0 : -1}
              className="grid-cell grid-hcell grid-gutter"
              style={{ width: gutterWidth }}
            >
              <span className="visually-hidden">Row</span>
            </div>
            {columns[0] ? header(0, columns[0], false) : null}
            {spacer > 0 ? <div className="grid-spacer" aria-hidden="true" style={{ width: spacer }} /> : null}
            {columns.slice(first, win.columns.end).map((c, k) => header(first + k, c, false))}
            {headerExtra > 0 && columns[headerExtra] ? header(headerExtra, columns[headerExtra], true) : null}
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
        {data.capped ? <span className="muted">Sorted views show the first {formatInt(data.rowCount)} rows.</span> : null}
      </div>
      <dialog
        ref={dialog}
        className="dialog"
        aria-labelledby="copy-dialog-title"
        onClose={() => setManualCopy(null)}
      >
        <h2 id="copy-dialog-title" className="dialog-title">
          Copy by hand
        </h2>
        <p>The browser blocked the clipboard. The text is selected below: press Ctrl+C or Cmd+C.</p>
        <textarea className="dialog-text" readOnly aria-label="Text to copy" value={manualCopy ?? ""} rows={8} />
        <div className="dialog-actions">
          <button type="button" className="button" onClick={() => dialog.current?.close()}>
            Close
          </button>
        </div>
      </dialog>
    </div>
  );
}
