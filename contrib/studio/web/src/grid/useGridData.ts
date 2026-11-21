import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { ApiError, apiGet, apiPath } from "../api/client";
import type { ApiCell, CountResponse, RowsResponse } from "../api/types";
import { apiQuery, viewToSearch } from "./filters";
import type { ViewState } from "./filters";
import { PageCache, pageSize } from "./pageCache";
import type { PageMode } from "./pageCache";

/** The server pages by offset up to page 1000, so a sorted view reaches this many rows. */
export const maxOffsetRows = 1000 * pageSize;

export type CountState = "idle" | "loading" | "unavailable";

export interface GridData {
  /** How many rows to make room for: exact, or the best estimate so far. */
  rowCount: number;
  /** True while rowCount is an estimate. */
  approx: boolean;
  /** True when a sorted view hides rows past what offset paging reaches. */
  capped: boolean;
  /** Offset paging gives random access; keyset paging loads in order. */
  mode: PageMode;
  /** The view is read from its end (keyset order reversed, shown ascending). */
  tail: boolean;
  row: (index: number) => ApiCell[] | undefined;
  /** Changes every time rows arrive, so a view that reads them draws again. */
  version: number;
  want: (start: number, end: number) => void;
  /** No page has arrived yet. */
  loading: boolean;
  error: ApiError | null;
  retry: () => void;
  refresh: () => void;
  countState: CountState;
  /** The exact count, when known (asked for, or learned by reaching the last page). */
  exact: number | null;
  askCount: () => void;
  /**
   * Prepare to show the last row and say how many rows there are. An offset
   * view has random access, so nothing changes; a keyset view counts the rows
   * and reads from the end. Null when the count is not available.
   */
  jumpToEnd: () => Promise<number | null>;
  /** Leave the tail and read from the start again. */
  leaveTail: () => void;
}

export interface GridDataOptions {
  source: string;
  table: string;
  view: ViewState;
  hasPrimaryKey: boolean;
  /** The estimate from the table list; negative when unknown. */
  estimate: number;
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, "network", "Studio could not be reached.");
}

interface Counted {
  key: string;
  state: CountState;
  n: number | null;
}

/** useGridData owns the page cache of one table view and the row count shown for it. */
export function useGridData(options: GridDataOptions): GridData {
  const { source, table, view, hasPrimaryKey, estimate } = options;
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [cache, setCache] = useState<PageCache<ApiCell[]> | null>(null);
  const [counted, setCounted] = useState<Counted | null>(null);
  const [tailFor, setTailFor] = useState<string | null>(null);
  const versionRef = useRef(0);

  const mode: PageMode = view.sort === null && hasPrimaryKey ? "keyset" : "offset";
  const viewKey = `${source}|${table}|${viewToSearch(view)}`;
  const viewKeyRef = useRef(viewKey);
  viewKeyRef.current = viewKey;

  const count = counted && counted.key === viewKey ? counted : null;
  const asked = count?.n ?? null;
  const tail = mode === "keyset" && tailFor === viewKey && asked !== null;
  const key = `${viewKey}|${tail ? "tail" : "head"}`;

  useEffect(() => {
    const created = new PageCache<ApiCell[]>({
      mode,
      onChange: () => {
        versionRef.current++;
        bump();
      },
      fetchPage: async (request, signal) => {
        const extra: Record<string, string> = { perPage: String(request.perPage) };
        if (request.after !== undefined) {
          extra.after = request.after;
        }
        if (request.page !== undefined) {
          extra.page = String(request.page);
        }
        if (mode === "keyset" && tail) {
          extra.desc = "1";
        }
        const res = await apiGet<RowsResponse>(
          apiPath("sources", source, "tables", table, "rows") + apiQuery(view, extra),
          signal,
        );
        return { rows: res.rows, next: res.next, hasMore: res.hasMore };
      },
    });
    setCache(created);

    return () => {
      created.dispose();
      setCache(null);
    };
    // The key stands for source, table, view and tail.
  }, [key, mode]);

  const askCount = useCallback(() => {
    const forKey = viewKeyRef.current;
    setCounted({ key: forKey, state: "loading", n: null });
    return apiGet<CountResponse>(apiPath("sources", source, "tables", table, "count") + apiQuery(view)).then(
      (res) => {
        if (viewKeyRef.current !== forKey) {
          return null;
        }
        const n = res.count ?? null;
        setCounted({ key: forKey, state: n === null ? "unavailable" : "idle", n });
        return n;
      },
      () => {
        if (viewKeyRef.current === forKey) {
          setCounted({ key: forKey, state: "unavailable", n: null });
        }
        return null;
      },
    );
    // view is part of the key.
  }, [source, table, viewKey]);

  const learned = !tail ? (cache?.exactCount ?? null) : null;
  const exact = asked ?? learned;
  const known = cache?.knownRows ?? 0;

  const jumpToEnd = useCallback(async (): Promise<number | null> => {
    if (mode === "offset") {
      return Math.min(exactRef.current ?? rowCountRef.current, maxOffsetRows);
    }
    const forKey = viewKeyRef.current;
    const n = asked ?? (await askCount());
    if (n !== null && viewKeyRef.current === forKey) {
      setTailFor(forKey);
    }
    return n;
  }, [mode, asked, askCount]);

  const exactRef = useRef<number | null>(null);
  const rowCountRef = useRef(0);
  exactRef.current = exact;

  const leaveTail = useCallback(() => setTailFor(null), []);

  let rowCount: number;
  let approx: boolean;

  if (exact !== null) {
    rowCount = exact;
    approx = false;
  } else if (estimate >= 0 && estimate >= known) {
    rowCount = estimate;
    approx = true;
  } else {
    // The estimate is unknown or already beaten: room for what is loaded plus one more page.
    rowCount = known + pageSize;
    approx = true;
  }

  let capped = false;
  if (mode === "offset" && rowCount > maxOffsetRows) {
    rowCount = maxOffsetRows;
    capped = true;
  }

  rowCountRef.current = rowCount;

  // Displayed row i is the cache row rowCount - 1 - i while reading from the end.
  const want = useCallback(
    (start: number, end: number) => {
      if (tail) {
        cache?.want(Math.max(rowCount - end, 0), Math.max(rowCount - start, 1));
      } else {
        cache?.want(start, end);
      }
    },
    [cache, tail, rowCount],
  );
  const row = useCallback(
    (index: number) => (tail ? cache?.row(rowCount - 1 - index) : cache?.row(index)),
    [cache, tail, rowCount],
  );
  const retry = useCallback(() => cache?.retry(), [cache]);
  const refresh = useCallback(() => cache?.refresh(), [cache]);

  return {
    rowCount,
    approx,
    capped,
    mode,
    tail,
    row,
    version: versionRef.current,
    want,
    loading: cache === null || (known === 0 && cache.loadedPages === 0 && cache.error === null),
    error: cache?.error ? asApiError(cache.error) : null,
    retry,
    refresh,
    countState: count?.state ?? "idle",
    exact,
    askCount: () => void askCount(),
    jumpToEnd,
    leaveTail,
  };
}
