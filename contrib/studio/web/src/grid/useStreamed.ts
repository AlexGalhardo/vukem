import { useCallback, useEffect, useReducer, useRef } from "react";
import type { ApiCell } from "../api/types";
import { StreamStore } from "./stream";
import type { FetchChunk } from "./stream";
import type { GridData } from "./useGridData";

export interface StreamedOptions {
  /** Changes when the list must start over (another collection, filter, sort or pattern). */
  resetKey: string;
  fetchChunk: FetchChunk;
  keyOf?: (row: ApiCell[]) => string | undefined;
  /** Load the next step when the viewport nears the end. Without it a button asks for each step. */
  auto: boolean;
}

export interface Streamed {
  data: GridData;
  /** The rows loaded so far, in the order they came. */
  rows: readonly ApiCell[][];
  loaded: number;
  hasMore: boolean;
  loadingMore: boolean;
  duplicates: number;
  steps: number;
  loadMore: () => void;
}

const lookahead = 30;

/** useStreamed gives the grid a list that arrives in steps: the rows so far and, while there is more, room for one more. */
export function useStreamed(options: StreamedOptions): Streamed {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const latest = useRef(options);
  latest.current = options;
  const store = useRef<StreamStore | null>(null);

  if (store.current === null) {
    store.current = new StreamStore({
      fetchChunk: (cursor, step, signal) => latest.current.fetchChunk(cursor, step, signal),
      keyOf: (row) => latest.current.keyOf?.(row),
      onChange: () => bump(),
    });
  }
  const s = store.current;

  useEffect(() => {
    s.reset();
    void s.loadMore();
    // The key stands for everything the list depends on.
  }, [options.resetKey, s]);

  useEffect(() => () => s.reset(), [s]);

  const loadMore = useCallback(() => void s.loadMore(), [s]);
  const want = useCallback(
    (_start: number, end: number) => {
      if (latest.current.auto && end >= s.rows.length - lookahead) {
        void s.loadMore();
      }
    },
    [s],
  );

  const rows = s.rows;
  const more = s.hasMore;
  const rowCount = rows.length + (options.auto && more ? 1 : 0);

  const data: GridData = {
    rowCount,
    approx: more,
    capped: false,
    mode: "offset",
    tail: false,
    row: (index) => rows[index],
    version: s.version,
    want,
    loading: s.loading && rows.length === 0,
    error: s.error,
    retry: () => s.retry(),
    refresh: () => {
      s.reset();
      void s.loadMore();
    },
    countState: "idle",
    exact: more ? null : rows.length,
    askCount: () => undefined,
    jumpToEnd: () => Promise.resolve(rows.length),
    leaveTail: () => undefined,
    patchCell: () => undefined,
    reload: () => {
      s.reset();
      void s.loadMore();
    },
  };

  return { data, rows, loaded: rows.length, hasMore: more, loadingMore: s.loading, duplicates: s.duplicates, steps: s.steps, loadMore };
}
