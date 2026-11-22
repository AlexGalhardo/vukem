import type { ApiError } from "../api/client";
import type { ApiCell } from "../api/types";
import type { PageSize } from "./paging";

export type CountState = "idle" | "loading" | "unavailable";

/** The state of a paged view: which page, how many pages, and how to move between them. */
export interface Paging {
  /** The page asked for, from 1. */
  page: number;
  size: PageSize;
  /** The exact row count of the view, when known. */
  total: number | null;
  /** The estimate of the table, shown as "about" while the exact count is not known. */
  estimate: number | null;
  /** Null while the count is not known. */
  pageCount: number | null;
  hasPrev: boolean;
  hasNext: boolean;
  /** The last page is known only once the count is. */
  canLast: boolean;
  /** A page is being fetched. */
  pending: boolean;
  /** The view has more rows than can be paged through (a sorted view stops at 100,000). */
  capped: boolean;
  rowCap: number;
  countState: CountState;
  /** Count again, after the count ran out of time. */
  askCount: () => void;
  goTo: (page: number) => void;
  setSize: (size: PageSize) => void;
}

/** What the grid draws: the rows it holds and, for a paged view, where they sit in the whole. */
export interface GridData {
  /** How many rows the grid holds: one page, or everything of a list that is all in memory. */
  rowCount: number;
  /** Rows that come before the first held row in the whole view (0 unless paged). */
  offset: number;
  /** The row count of the whole view, when known. */
  total: number | null;
  /** True while the count shown is an estimate or a lower bound. */
  approx: boolean;
  row: (index: number) => ApiCell[] | undefined;
  /** Changes every time rows arrive, so a view that reads them draws again. */
  version: number;
  /** Ask for the rows between two indexes: a list that arrives in steps reads ahead; a page has nothing to do. */
  want: (start: number, end: number) => void;
  /** No rows have arrived yet. */
  loading: boolean;
  error: ApiError | null;
  retry: () => void;
  refresh: () => void;
  /**
   * Prepare to show the last row of the whole view. A paged view moves to its
   * last page (moved is true: the rows change) and says the number of that row;
   * a list in memory has nothing to move. Null when the last row cannot be
   * reached: the count is not available.
   */
  jumpToEnd: () => Promise<{ moved: boolean; last: number } | null>;
  /** Back to the first row of the whole view: the first page. */
  jumpToStart: () => void;
  /** Put a saved value into the held row, so the grid shows it without a new request. */
  patchCell: (row: number, column: number, cell: ApiCell) => void;
  /** Read again after a row was added or removed. */
  reload: () => void;
  /** Present for a paged view: the grid draws the pager and all the rows of the page. */
  paging?: Paging;
}
