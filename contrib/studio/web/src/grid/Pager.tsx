import type { ReactElement } from "react";
import { formatInt } from "./format";
import type { GridData, Paging } from "./gridData";
import { pageSizes, parsePageSize } from "./paging";

/** rangeText says which rows the page holds and how many there are in all. */
export function rangeText(data: GridData, paging: Paging): string {
  if (data.rowCount === 0) {
    return data.loading ? "Loading rows…" : "No rows";
  }
  const range = `Rows ${formatInt(data.offset + 1)} to ${formatInt(data.offset + data.rowCount)}`;
  if (paging.total !== null) {
    return `${range} of ${formatInt(paging.total)}`;
  }
  return paging.estimate !== null ? `${range} of about ${formatInt(paging.estimate)}` : range;
}

/** Pager is the footer of a paged grid: rows per page, the range shown, and the page controls. */
export function Pager({ data, paging }: { data: GridData; paging: Paging }): ReactElement {
  const last = paging.pageCount;

  return (
    <nav className="pager" aria-label="Pagination">
      <label className="field-inline">
        <span>Rows per page</span>
        <select value={paging.size} onChange={(e) => paging.setSize(parsePageSize(e.target.value))}>
          {pageSizes.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <span className="pager-range" data-testid="grid-position" role="status">
        {rangeText(data, paging)}
      </span>
      <div className="pager-buttons" role="group" aria-label="Page controls">
        <button
          type="button"
          className="button button-small"
          aria-label="First page"
          disabled={!paging.hasPrev}
          onClick={() => paging.goTo(1)}
        >
          First
        </button>
        <button
          type="button"
          className="button button-small"
          aria-label="Previous page"
          disabled={!paging.hasPrev}
          onClick={() => paging.goTo(paging.page - 1)}
        >
          Previous
        </button>
        <span className="pager-page" data-testid="grid-page">
          Page {formatInt(paging.page)}
          {last !== null ? ` of ${formatInt(last)}` : ""}
        </span>
        <button
          type="button"
          className="button button-small"
          aria-label="Next page"
          disabled={!paging.hasNext}
          onClick={() => paging.goTo(paging.page + 1)}
        >
          Next
        </button>
        <button
          type="button"
          className="button button-small"
          aria-label="Last page"
          disabled={!paging.canLast}
          onClick={() => last !== null && paging.goTo(last)}
        >
          Last
        </button>
      </div>
      {paging.countState === "unavailable" ? (
        <span className="muted" role="status">
          The exact count is not available: counting took too long.{" "}
          <button type="button" className="button button-small" onClick={paging.askCount}>
            Count again
          </button>
        </span>
      ) : null}
      {paging.capped ? (
        <span className="muted">
          Pages stop at row {formatInt(paging.rowCap)}; filter the view to reach the rest, or export it whole.
        </span>
      ) : null}
    </nav>
  );
}
