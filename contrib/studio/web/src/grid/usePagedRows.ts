import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../api/client";
import type { ApiCell } from "../api/types";
import type { CountState, GridData, Paging } from "./gridData";
import { clampPage, pageCount, pageOffset, savePageSize, storedPageSize } from "./paging";
import type { PageSize } from "./paging";

export interface PageFetch {
  rows: ApiCell[][];
  /** The server saw at least one more row after this page. */
  hasMore: boolean;
}

export interface PagedOptions {
  /** Everything the rows depend on except the page and its size: source, table, filters, sort. A change returns to page 1. */
  resetKey: string;
  fetchPage: (page: number, size: PageSize, signal: AbortSignal) => Promise<PageFetch>;
  /** The exact count of the view, or null when the server could not finish it in time. */
  fetchTotal: (signal: AbortSignal) => Promise<number | null>;
  /** How many rows of the view can be paged through. */
  rowCap: number;
  /** The estimate of the table; negative when unknown. Used only for the "about" count before the exact one lands. */
  estimate: number;
}

interface Loaded {
  resetKey: string;
  page: number;
  size: PageSize;
  rows: ApiCell[][];
  hasMore: boolean;
}

interface Counted {
  resetKey: string;
  state: CountState;
  n: number | null;
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, "network", "Studio could not be reached.");
}

function browserStore(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

const noStore = { getItem: (): string | null => null, setItem: (): void => undefined };

/**
 * usePagedRows reads a view one page at a time. Only the current page is
 * fetched, with a page size the server bounds; the exact count is asked
 * separately, so "Rows 26 to 27 of 27" and the last page are known. The page
 * returns to 1 when the view changes (filters, sort, page size), goes back to
 * the last valid page when rows were removed under it, and stays put when
 * the rows are read again (Live, refresh, an edit).
 */
export function usePagedRows(options: PagedOptions): GridData {
  const { resetKey, rowCap, estimate } = options;
  const latest = useRef(options);
  latest.current = options;

  const [size, setSizeState] = useState<PageSize>(() => storedPageSize(browserStore() ?? noStore));
  const [page, setPage] = useState(1);
  const [seenKey, setSeenKey] = useState(resetKey);
  const [nonce, setNonce] = useState(0);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [pending, setPending] = useState(true);
  const [counted, setCounted] = useState<Counted | null>(null);
  const [countNonce, setCountNonce] = useState(0);
  const version = useRef(0);

  // A new view starts at its first page. Set during render so no request goes out for the old page of the new view.
  let current = page;
  if (seenKey !== resetKey) {
    setSeenKey(resetKey);
    setPage(1);
    current = 1;
  }

  const shown = loaded !== null && loaded.resetKey === resetKey ? loaded : null;
  const count = counted !== null && counted.resetKey === resetKey ? counted : null;
  const total = count?.n ?? null;
  const pages = total !== null ? pageCount(total, size, rowCap) : null;
  const totalRef = useRef<number | null>(null);
  totalRef.current = total;

  const goTo = useCallback(
    (next: number) => setPage(clampPage(next, pageCount(totalRef.current ?? Number.POSITIVE_INFINITY, size, rowCap))),
    [size, rowCap],
  );

  const setSize = useCallback((next: PageSize) => {
    setSizeState(next);
    setPage(1);
    savePageSize(browserStore() ?? noStore, next);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setPending(true);
    setError(null);

    latest.current.fetchPage(current, size, controller.signal).then(
      (res) => {
        if (controller.signal.aborted) {
          return;
        }
        setPending(false);
        if (res.rows.length === 0 && current > 1) {
          // The rows under this page went away (a delete, a filter that matches less): the last valid page.
          const known = totalRef.current;
          setPage(Math.max(Math.min(current - 1, known !== null ? pageCount(known, size, rowCap) : current - 1), 1));
          return;
        }
        version.current++;
        setLoaded({ resetKey, page: current, size, rows: res.rows, hasMore: res.hasMore });
      },
      (err: unknown) => {
        if (!controller.signal.aborted) {
          setPending(false);
          setError(asApiError(err));
        }
      },
    );

    return () => controller.abort();
    // resetKey stands for the view; fetchPage is read through the ref.
  }, [resetKey, current, size, nonce, rowCap]);

  useEffect(() => {
    const controller = new AbortController();
    setCounted((prev) => ({ resetKey, state: "loading", n: prev?.resetKey === resetKey ? prev.n : null }));

    latest.current.fetchTotal(controller.signal).then(
      (n) => {
        if (!controller.signal.aborted) {
          setCounted({ resetKey, state: n === null ? "unavailable" : "idle", n });
        }
      },
      () => {
        if (!controller.signal.aborted) {
          setCounted({ resetKey, state: "unavailable", n: null });
        }
      },
    );

    return () => controller.abort();
  }, [resetKey, countNonce]);

  // Rows were removed under the page, or the size grew: stay inside the pages that exist.
  useEffect(() => {
    if (pages !== null && current > pages) {
      setPage(pages);
    }
  }, [pages, current]);

  const refresh = useCallback(() => {
    setNonce((n) => n + 1);
    setCountNonce((n) => n + 1);
  }, []);

  const retry = useCallback(() => setNonce((n) => n + 1), []);
  const askCount = useCallback(() => setCountNonce((n) => n + 1), []);

  const rows = shown?.rows;
  const offset = shown ? pageOffset(shown.page, shown.size) : 0;
  const lastPage = pages ?? Math.max(Math.ceil(rowCap / size), 1);
  const hasNext = current < lastPage && (total !== null || (shown?.page === current && shown.hasMore));

  const paging: Paging = {
    page: current,
    size,
    total,
    estimate: total === null && estimate >= 0 ? estimate : null,
    pageCount: pages,
    hasPrev: current > 1,
    hasNext,
    canLast: pages !== null && current < pages,
    pending,
    capped: total !== null && total > rowCap,
    rowCap,
    countState: count?.state ?? "idle",
    askCount,
    goTo,
    setSize,
  };

  const jumpToEnd = useCallback((): Promise<{ moved: boolean; last: number } | null> => {
    const known = totalRef.current;
    if (known === null) {
      return Promise.resolve(null);
    }
    // The last row is the last reachable one; the grid clamps its focus to the rows the last page holds.
    const target = pageCount(known, size, rowCap);
    const moved = target !== current;
    if (moved) {
      setPage(target);
    }
    return Promise.resolve({ moved, last: Math.min(known, rowCap) });
  }, [rowCap, size, current]);

  const jumpToStart = useCallback(() => setPage(1), []);

  const patchCell = useCallback(
    (index: number, column: number, cell: ApiCell) => {
      setLoaded((prev) => {
        const row = prev?.rows[index];
        if (!prev || !row) {
          return prev;
        }
        version.current++;
        const rowsNext = prev.rows.slice();
        rowsNext[index] = row.map((c, i) => (i === column ? cell : c));
        return { ...prev, rows: rowsNext };
      });
    },
    [],
  );

  return {
    rowCount: rows?.length ?? 0,
    offset,
    total,
    approx: total === null,
    row: (index) => rows?.[index],
    version: version.current,
    want: () => undefined,
    loading: shown === null && error === null,
    error,
    retry,
    refresh,
    jumpToEnd,
    jumpToStart,
    patchCell,
    reload: refresh,
    paging,
  };
}
