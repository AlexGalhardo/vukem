import { describe, expect, test } from "bun:test";
import {
  columnOffsets,
  computeWindow,
  maxScrollSize,
  rowHeights,
  rowTop,
  scaleFor,
  scrollLeftToShowColumn,
  scrollTopToShowRow,
  visibleColumns,
  visibleRows,
} from "./virtual";

const widths = (n: number, w = 100): number[] => Array.from({ length: n }, () => w);

describe("visibleRows", () => {
  test("an empty table renders nothing", () => {
    expect(visibleRows(0, 32, 0, 600)).toEqual({ start: 0, end: 0 });
  });

  test("one row renders that row", () => {
    expect(visibleRows(1, 32, 0, 600)).toEqual({ start: 0, end: 1 });
  });

  test("the top of a huge table renders the viewport plus overscan below", () => {
    // 600 / 32 = 18.75 rows, rounded up to 19, plus 6 of overscan.
    expect(visibleRows(100_000, 32, 0, 600)).toEqual({ start: 0, end: 25 });
  });

  test("the middle has overscan on both sides", () => {
    const r = visibleRows(100_000, 32, 32 * 50_000, 640);
    expect(r).toEqual({ start: 50_000 - 6, end: 50_000 + 20 + 6 });
  });

  test("the end is clamped to the row count", () => {
    const r = visibleRows(1000, 32, 1000 * 32 - 600, 600);
    expect(r.end).toBe(1000);
    expect(r.start).toBe(Math.floor((1000 * 32 - 600) / 32) - 6);
  });

  test("never renders more than about a screen of rows", () => {
    for (const rowCount of [100, 100_000, 1_000_000, 50_000_000]) {
      for (const scrollTop of [0, 12_345, 3_000_000]) {
        const r = visibleRows(rowCount, 28, scrollTop, 1080);
        expect(r.end - r.start <= 100).toBe(true);
      }
    }
  });

  test("a negative scroll (rubber banding) acts as zero", () => {
    expect(visibleRows(100, 32, -50, 320)).toEqual(visibleRows(100, 32, 0, 320));
  });
});

describe("density", () => {
  test("a different row height changes the range", () => {
    const compact = visibleRows(10_000, rowHeights.compact, 0, 560);
    const comfortable = visibleRows(10_000, rowHeights.comfortable, 0, 560);
    expect(compact.end).toBe(20 + 6);
    expect(comfortable.end).toBe(14 + 6);
  });

  test("the heights are 28, 32 and 40", () => {
    expect(rowHeights).toEqual({ compact: 28, normal: 32, comfortable: 40 });
  });
});

describe("visibleColumns", () => {
  test("30 columns or fewer are all rendered", () => {
    expect(visibleColumns(widths(30), 500, 300)).toEqual({ start: 0, end: 30 });
    expect(visibleColumns(widths(1), 0, 300)).toEqual({ start: 0, end: 1 });
    expect(visibleColumns([], 0, 300)).toEqual({ start: 0, end: 0 });
  });

  test("31 columns are virtualized with an overscan of 2", () => {
    expect(visibleColumns(widths(31), 0, 350)).toEqual({ start: 0, end: 3 + 1 + 2 });
    expect(visibleColumns(widths(31), 1000, 350)).toEqual({ start: 10 - 2, end: 13 + 1 + 2 });
  });

  test("uneven widths are followed", () => {
    const w = [50, 200, 50, 200, ...widths(30, 100)];
    const r = visibleColumns(w, 260, 100);
    // 260 is inside column 1 (50 to 250)? No: column 2 spans 250 to 300; 359 is in column 3 (300 to 500).
    expect(r).toEqual({ start: 0, end: 3 + 1 + 2 });
  });

  test("the end of the strip is clamped", () => {
    expect(visibleColumns(widths(40), 3700, 300)).toEqual({ start: 35, end: 40 });
  });
});

describe("computeWindow", () => {
  test("reports the sizes", () => {
    const w = computeWindow({
      scrollTop: 0,
      scrollLeft: 0,
      viewportHeight: 320,
      viewportWidth: 500,
      rowCount: 1000,
      rowHeight: 32,
      columnWidths: [100, 200],
    });
    expect(w.totalHeight).toBe(32_000);
    expect(w.totalWidth).toBe(300);
    expect(w.scale).toBe(1);
    expect(w.rows).toEqual({ start: 0, end: 16 });
    expect(w.columns).toEqual({ start: 0, end: 2 });
  });

  test("an empty table has no height", () => {
    const w = computeWindow({
      scrollTop: 0,
      scrollLeft: 0,
      viewportHeight: 320,
      viewportWidth: 500,
      rowCount: 0,
      rowHeight: 32,
      columnWidths: [100],
    });
    expect(w.totalHeight).toBe(0);
    expect(w.rows).toEqual({ start: 0, end: 0 });
  });
});

describe("capped scroll size", () => {
  test("a table taller than the cap is scaled down", () => {
    expect(scaleFor(1_000_000, 40, 600) > 3.9).toBe(true);
    expect(scaleFor(100_000, 32, 600)).toBe(1);
    const w = computeWindow({
      scrollTop: 0,
      scrollLeft: 0,
      viewportHeight: 600,
      viewportWidth: 600,
      rowCount: 1_000_000,
      rowHeight: 40,
      columnWidths: [100],
    });
    expect(w.totalHeight).toBe(maxScrollSize);
  });

  test("the last scroll position reaches the last row", () => {
    const scrollTop = maxScrollSize - 600;
    const r = visibleRows(1_000_000, 40, scrollTop, 600);
    expect(r.end).toBe(1_000_000);
    expect(visibleRows(1_000_000, 40, 0, 600).start).toBe(0);
  });

  test("rows are drawn relative to the scroll position", () => {
    const scale = scaleFor(1_000_000, 40, 600);
    const scrollTop = 2_000_000;
    const first = Math.floor((scrollTop * scale) / 40);
    const top = rowTop(first, 40, scrollTop, scale);
    expect(top <= scrollTop && top > scrollTop - 40).toBe(true);
    expect(rowTop(first + 1, 40, scrollTop, scale) - top).toBe(40);
  });

  test("unscaled rows sit at index times height", () => {
    expect(rowTop(10, 32, 999, 1)).toBe(320);
  });
});

describe("scrollTopToShowRow", () => {
  test("keeps the position when the row is visible", () => {
    expect(scrollTopToShowRow(5, 32, 0, 320, 1000)).toBe(0);
  });

  test("scrolls up to a row above", () => {
    expect(scrollTopToShowRow(2, 32, 640, 320, 1000)).toBe(64);
  });

  test("scrolls down to a row below, flush with the bottom", () => {
    expect(scrollTopToShowRow(20, 32, 0, 320, 1000)).toBe(21 * 32 - 320);
  });
});

describe("columns", () => {
  test("offsets are cumulative with the total last", () => {
    expect(columnOffsets([10, 20, 30])).toEqual([0, 10, 30, 60]);
  });

  test("scrollLeftToShowColumn accounts for the pinned column", () => {
    const offsets = columnOffsets(widths(40));
    expect(scrollLeftToShowColumn(5, offsets, 0, 400, 60)).toBe(200);
    expect(scrollLeftToShowColumn(5, offsets, 450, 400, 60)).toBe(440);
    expect(scrollLeftToShowColumn(5, offsets, 300, 400, 60)).toBe(300);
  });
});
