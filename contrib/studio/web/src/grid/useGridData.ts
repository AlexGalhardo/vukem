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
}

export interface GridDataOptions {
  source: string;
  table: string;
  view: ViewState;
  hasPrimaryKey: boolean;
  /** The estimate from the table list; negative when unknown. */
  estimate: number;
  /** Read the table from its end: keyset order reversed. Only with the default sort. */
  tail: boolean;
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, "network", "Studio could not be reached.");
}

/** useGridData owns the page cache of one table view and the row count shown for it. */
export function useGridData(options: GridDataOptions): GridData {
  const { source, table, view, hasPrimaryKey, estimate, tail } = options;
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [cache, setCache] = useState<PageCache<ApiCell[]> | null>(null);
  const [counted, setCounted] = useState<{ key: string; n: number } | null>(null);
  const [countState, setCountState] = useState<CountState>("idle");
  const versionRef = useRef(0);

  const mode: PageMode = view.sort === null && hasPrimaryKey ? "keyset" : "offset";
  const search = viewToSearch(view);
  const key = `${source}|${table}|${search}|${tail ? "tail" : "head"}`;
  const keyRef = useRef(key);
  keyRef.current = key;

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
    setCounted(null);
    setCountState("idle");

    return () => {
      created.dispose();
      setCache(null);
    };
    // The key stands for source, table, view and tail.
  }, [key, mode]);

  const want = useCallback((start: number, end: number) => cache?.want(start, end), [cache]);
  const retry = useCallback(() => cache?.retry(), [cache]);
  const refresh = useCallback(() => cache?.refresh(), [cache]);
  const row = useCallback((index: number) => cache?.row(index), [cache]);

  const askCount = useCallback(() => {
    const asked = keyRef.current;
    setCountState("loading");
    apiGet<CountResponse>(apiPath("sources", source, "tables", table, "count") + apiQuery(view)).then(
      (res) => {
        if (keyRef.current !== asked) {
          return;
        }
        if (res.count === undefined) {
          setCountState("unavailable");
          return;
        }
        setCounted({ key: asked, n: res.count });
        setCountState("idle");
      },
      () => {
        if (keyRef.current === asked) {
          setCountState("unavailable");
        }
      },
    );
    // view is part of the key.
  }, [source, table, key]);

  const learned = cache?.exactCount ?? null;
  const asked = counted && counted.key === key ? counted.n : null;
  const exact = asked ?? learned;
  const known = cache?.knownRows ?? 0;

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

  return {
    rowCount,
    approx,
    capped,
    mode,
    row,
    version: versionRef.current,
    want,
    loading: cache === null || (known === 0 && exact === null && cache.loadedPages === 0 && cache.error === null),
    error: cache?.error ? asApiError(cache.error) : null,
    retry,
    refresh,
    countState,
    exact,
    askCount,
  };
}
