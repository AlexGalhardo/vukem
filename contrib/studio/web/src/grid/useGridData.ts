import { apiGet, apiPath } from "../api/client";
import type { CountResponse, RowsResponse } from "../api/types";
import { apiQuery, viewToSearch } from "./filters";
import type { ViewState } from "./filters";
import type { GridData } from "./gridData";
import { orderedRowCap, sortedRowCap } from "./paging";
import { usePagedRows } from "./usePagedRows";

export interface GridDataOptions {
  source: string;
  table: string;
  view: ViewState;
  hasPrimaryKey: boolean;
  /** The estimate from the table list; negative when unknown. */
  estimate: number;
}

/**
 * useGridData reads one page of a table at a time: the rows of the page
 * (`perPage` and `page` go to the server, which bounds the size) and, apart,
 * the exact count of the view. A view in primary key order can be paged a
 * million rows deep, a sorted one 100,000.
 */
export function useGridData(options: GridDataOptions): GridData {
  const { source, table, view, hasPrimaryKey, estimate } = options;

  return usePagedRows({
    resetKey: `${source}|${table}|${viewToSearch(view)}`,
    fetchPage: async (page, size, signal) => {
      const res = await apiGet<RowsResponse>(
        apiPath("sources", source, "tables", table, "rows") + apiQuery(view, { perPage: String(size), page: String(page) }),
        signal,
      );
      return { rows: res.rows, hasMore: res.hasMore };
    },
    fetchTotal: async (signal) => {
      const res = await apiGet<CountResponse>(apiPath("sources", source, "tables", table, "count") + apiQuery(view), signal);
      return res.count ?? null;
    },
    rowCap: view.sort === null && hasPrimaryKey ? orderedRowCap : sortedRowCap,
    estimate,
  });
}
