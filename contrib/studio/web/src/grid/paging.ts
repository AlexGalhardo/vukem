/**
 * The arithmetic of the paged grid, kept free of React and the DOM so a table
 * can check it. A page is `size` rows; pages count from 1.
 */

/** The page sizes the interface offers. The server refuses more than the largest. */
export const pageSizes = [25, 50, 75, 100] as const;
export type PageSize = (typeof pageSizes)[number];

export const defaultPageSize: PageSize = 25;

/**
 * How many rows of a view a person can page through. They mirror the server's
 * limits (maxSortedRows, maxOrderedRows): a deep page is read with OFFSET or
 * skip, which reads and drops every row before it. The primary key (or _id)
 * order walks an index, so it reaches further than a sorted one.
 */
export const sortedRowCap = 100_000;
export const orderedRowCap = 1_000_000;

export const pageSizeKey = "studio.pageSize";

/** parsePageSize accepts only a size the interface offers; anything else is the default. */
export function parsePageSize(raw: unknown): PageSize {
  const n = typeof raw === "string" ? Number(raw) : raw;
  return pageSizes.find((s) => s === n) ?? defaultPageSize;
}

interface Store {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** storedPageSize reads the size this browser chose, or the default. */
export function storedPageSize(store: Store): PageSize {
  try {
    return parsePageSize(store.getItem(pageSizeKey));
  } catch {
    return defaultPageSize;
  }
}

export function savePageSize(store: Store, size: PageSize): void {
  try {
    store.setItem(pageSizeKey, String(size));
  } catch {
    // Storage may be blocked; the choice then lasts until reload.
  }
}

/** reachableRows is how many of total rows can be paged through. */
export function reachableRows(total: number, cap: number): number {
  return Math.min(Math.max(total, 0), cap);
}

/** pageCount is the number of pages for a total, at least 1 so an empty view still has a page. */
export function pageCount(total: number, size: number, cap: number = Number.POSITIVE_INFINITY): number {
  return Math.max(Math.ceil(reachableRows(total, cap) / size), 1);
}

/** clampPage keeps a page inside 1..count. */
export function clampPage(page: number, count: number): number {
  return Math.min(Math.max(Math.trunc(page) || 1, 1), Math.max(count, 1));
}

/** pageOffset is how many rows come before a page. */
export function pageOffset(page: number, size: number): number {
  return (Math.max(page, 1) - 1) * size;
}

/** rowsOnLastPage is how many rows the last page holds. */
export function rowsOnLastPage(total: number, size: number, cap: number = Number.POSITIVE_INFINITY): number {
  const rows = reachableRows(total, cap);
  return rows === 0 ? 0 : rows - pageOffset(pageCount(total, size, cap), size);
}
