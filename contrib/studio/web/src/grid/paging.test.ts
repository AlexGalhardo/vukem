import { describe, expect, test } from "bun:test";
import {
  clampPage,
  defaultPageSize,
  orderedRowCap,
  pageCount,
  pageOffset,
  pageSizeKey,
  pageSizes,
  parsePageSize,
  reachableRows,
  rowsOnLastPage,
  savePageSize,
  sortedRowCap,
  storedPageSize,
} from "./paging";

describe("page sizes", () => {
  test("exactly 25, 50, 75 and 100, with 25 the default", () => {
    expect([...pageSizes]).toEqual([25, 50, 75, 100]);
    expect(defaultPageSize).toBe(25);
  });

  test("a stored value that is not one of them falls back to the default", () => {
    for (const raw of ["25", "50", "75", "100", 50, 100]) {
      expect(parsePageSize(raw)).toBe(Number(raw) as 25);
    }
    for (const raw of [null, undefined, "", "0", "10", "101", "200", "-25", "abc", "25.5", NaN, {}, [], 1000]) {
      expect(parsePageSize(raw)).toBe(25);
    }
  });

  test("the choice is remembered per browser and a blocked store does not break it", () => {
    const data = new Map<string, string>();
    const store = { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) };
    expect(storedPageSize(store)).toBe(25);
    savePageSize(store, 75);
    expect(data.get(pageSizeKey)).toBe("75");
    expect(storedPageSize(store)).toBe(75);
    data.set(pageSizeKey, "13");
    expect(storedPageSize(store)).toBe(25);

    const blocked = {
      getItem: (): string | null => {
        throw new Error("blocked");
      },
      setItem: (): void => {
        throw new Error("blocked");
      },
    };
    expect(storedPageSize(blocked)).toBe(25);
    expect(() => savePageSize(blocked, 50)).not.toThrow();
  });
});

describe("page math", () => {
  test("27 rows at 25 a page is 2 pages, the second with 2 rows", () => {
    expect(pageCount(27, 25)).toBe(2);
    expect(rowsOnLastPage(27, 25)).toBe(2);
    expect(pageOffset(2, 25)).toBe(25);
  });

  test("an exact multiple has no empty last page", () => {
    expect(pageCount(50, 25)).toBe(2);
    expect(rowsOnLastPage(50, 25)).toBe(25);
    expect(pageCount(51, 25)).toBe(3);
    expect(rowsOnLastPage(51, 25)).toBe(1);
    expect(pageCount(100, 100)).toBe(1);
    expect(pageCount(101, 100)).toBe(2);
  });

  test("an empty view still has one page with no rows", () => {
    expect(pageCount(0, 25)).toBe(1);
    expect(rowsOnLastPage(0, 25)).toBe(0);
    expect(pageCount(-3, 25)).toBe(1);
  });

  test("changing the size keeps the page count consistent with the total", () => {
    for (const size of pageSizes) {
      for (const total of [0, 1, 24, 25, 26, 99, 100, 101, 999, 1000, 100_001]) {
        const count = pageCount(total, size);
        const lastRows = rowsOnLastPage(total, size);
        expect((count - 1) * size + lastRows).toBe(total);
        expect(lastRows).toBeLessThanOrEqual(size);
      }
    }
  });

  test("a page past the end, or before the start, is clamped to a valid one", () => {
    expect(clampPage(9, 2)).toBe(2);
    expect(clampPage(0, 2)).toBe(1);
    expect(clampPage(-4, 2)).toBe(1);
    expect(clampPage(NaN, 2)).toBe(1);
    expect(clampPage(1.9, 3)).toBe(1);
    expect(clampPage(3, 0)).toBe(1);
  });

  test("the row cap limits the pages: a sorted view stops at 100,000 rows, a primary key view at a million", () => {
    expect(reachableRows(250_000, sortedRowCap)).toBe(100_000);
    expect(pageCount(250_000, 25, sortedRowCap)).toBe(4000);
    expect(pageCount(250_000, 100, sortedRowCap)).toBe(1000);
    expect(rowsOnLastPage(250_000, 75, sortedRowCap)).toBe(25);
    expect(pageCount(250_000, 25, orderedRowCap)).toBe(10_000);
    expect(pageCount(5_000_000, 100, orderedRowCap)).toBe(10_000);
  });
});
