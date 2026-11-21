/**
 * Window math for the grid. Everything here is pure: the scroll position and
 * the sizes go in, the index ranges to render come out. Rows have a fixed
 * height (long text is clipped), so no measuring is needed.
 */

export type Density = "compact" | "normal" | "comfortable";

export const rowHeights: Record<Density, number> = { compact: 28, normal: 32, comfortable: 40 };

export const rowOverscan = 6;
export const columnOverscan = 2;

/** Columns are virtualized only above this many. */
export const columnVirtualizationThreshold = 30;

/**
 * Browsers cap the height of a scrollable element (about 33 million pixels in
 * Chrome, less in Firefox). Past this size the scroll offset is scaled, so a
 * table of millions of rows is still reachable.
 */
export const maxScrollSize = 10_000_000;

/** A half-open range of indexes: start is included, end is not. */
export interface Range {
  start: number;
  end: number;
}

export interface WindowInput {
  scrollTop: number;
  scrollLeft: number;
  /** The visible height of the body (without the sticky header). */
  viewportHeight: number;
  viewportWidth: number;
  rowCount: number;
  rowHeight: number;
  columnWidths: readonly number[];
}

export interface GridWindow {
  rows: Range;
  columns: Range;
  /** The height to give the body, capped at maxScrollSize. */
  totalHeight: number;
  totalWidth: number;
  /** Real pixels per scrolled pixel of the (capped) body; 1 unless capped. */
  scale: number;
}

const empty: Range = { start: 0, end: 0 };

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(Math.max(n, lo), hi);
}

/**
 * scaleFor is the ratio between the real and the capped scroll range. The
 * ranges end where the last row meets the bottom of the viewport, so the last
 * scroll position always shows the last row.
 */
export function scaleFor(rowCount: number, rowHeight: number, viewportHeight: number): number {
  const real = rowCount * rowHeight;
  if (real <= maxScrollSize) {
    return 1;
  }
  const view = Math.min(Math.max(viewportHeight, 0), maxScrollSize - 1);
  return (real - view) / (maxScrollSize - view);
}

/** columnOffsets returns the left edge of every column plus the total as the last item. */
export function columnOffsets(widths: readonly number[]): number[] {
  const out: number[] = [0];
  let x = 0;
  for (const w of widths) {
    x += w;
    out.push(x);
  }
  return out;
}

/** The first index i with offsets[i + 1] > x: the column that holds position x. */
function columnAt(offsets: readonly number[], x: number): number {
  let lo = 0;
  let hi = offsets.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if ((offsets[mid + 1] ?? 0) > x) {
      hi = mid;
    } else {
      lo = mid + 1;
    }
  }
  return lo;
}

/** visibleColumns is the range of columns to render for the horizontal scroll. */
export function visibleColumns(widths: readonly number[], scrollLeft: number, viewportWidth: number): Range {
  const count = widths.length;
  if (count === 0) {
    return empty;
  }
  if (count <= columnVirtualizationThreshold) {
    return { start: 0, end: count };
  }

  const offsets = columnOffsets(widths);
  const first = columnAt(offsets, Math.max(scrollLeft, 0));
  const last = columnAt(offsets, Math.max(scrollLeft + viewportWidth - 1, 0));

  return {
    start: clamp(first - columnOverscan, 0, count),
    end: clamp(last + 1 + columnOverscan, 0, count),
  };
}

/** visibleRows is the range of rows to render for the vertical scroll. */
export function visibleRows(rowCount: number, rowHeight: number, scrollTop: number, viewportHeight: number): Range {
  if (rowCount <= 0 || rowHeight <= 0) {
    return empty;
  }

  const top = Math.max(scrollTop, 0) * scaleFor(rowCount, rowHeight, viewportHeight);
  const first = Math.floor(top / rowHeight);
  const last = Math.ceil((top + Math.max(viewportHeight, 0)) / rowHeight);

  return {
    start: clamp(first - rowOverscan, 0, rowCount),
    end: clamp(last + rowOverscan, 0, rowCount),
  };
}

/** computeWindow returns what to render for the current scroll position. */
export function computeWindow(input: WindowInput): GridWindow {
  const { rowCount, rowHeight, columnWidths } = input;

  return {
    rows: visibleRows(rowCount, rowHeight, input.scrollTop, input.viewportHeight),
    columns: visibleColumns(columnWidths, input.scrollLeft, input.viewportWidth),
    totalHeight: Math.min(rowCount * rowHeight, maxScrollSize),
    totalWidth: columnWidths.reduce((a, b) => a + b, 0),
    scale: scaleFor(rowCount, rowHeight, input.viewportHeight),
  };
}

/**
 * rowTop is where row index is drawn inside the body, in pixels. Unscaled it
 * is index * rowHeight; when the scroll size is capped the rows are drawn
 * relative to the scroll position, so the ones on screen still line up.
 */
export function rowTop(index: number, rowHeight: number, scrollTop: number, scale: number): number {
  if (scale === 1) {
    return index * rowHeight;
  }
  return scrollTop + (index * rowHeight - scrollTop * scale);
}

/**
 * scrollTopToShowRow returns the scrollTop that brings row index fully into
 * view with the least movement, or the current one when it already is.
 */
export function scrollTopToShowRow(
  index: number,
  rowHeight: number,
  scrollTop: number,
  viewportHeight: number,
  rowCount: number,
): number {
  const scale = scaleFor(rowCount, rowHeight, viewportHeight);
  const real = scrollTop * scale;
  const top = index * rowHeight;

  if (top < real) {
    return top / scale;
  }
  if (top + rowHeight > real + viewportHeight) {
    return (top + rowHeight - viewportHeight) / scale;
  }
  return scrollTop;
}

/** scrollLeftToShowColumn is scrollTopToShowRow for a column, with a pinned width at the left. */
export function scrollLeftToShowColumn(
  index: number,
  offsets: readonly number[],
  scrollLeft: number,
  viewportWidth: number,
  pinnedWidth: number,
): number {
  const left = offsets[index] ?? 0;
  const right = offsets[index + 1] ?? left;

  if (left - pinnedWidth < scrollLeft) {
    return Math.max(left - pinnedWidth, 0);
  }
  if (right > scrollLeft + viewportWidth) {
    return right - viewportWidth;
  }
  return scrollLeft;
}
