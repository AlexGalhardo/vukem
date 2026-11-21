import { useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { ApiCell, TableDetail, TablesResponse, TableSummary } from "../api/types";
import { useApi } from "../api/useApi";
import { ErrorNotice, Loading } from "../components/Notices";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { FilterBar } from "../grid/FilterBar";
import type { FilterRequest } from "../grid/FilterBar";
import { Grid } from "../grid/Grid";
import { nextSort, openFromSearch, searchToView, viewToSearch } from "../grid/filters";
import type { ViewState } from "../grid/filters";
import { foreignKeyLink } from "../grid/links";
import type { RowLink } from "../grid/links";
import { RowDrawer } from "../grid/RowDrawer";
import type { DrawerTarget } from "../grid/RowDrawer";
import { keyParts, packKey, unpackKey } from "../grid/rowKey";
import { useGridData } from "../grid/useGridData";
import type { Density } from "../grid/virtual";
import { Link, replaceSearch, useSearch } from "../lib/history";
import { useShortcut } from "../shortcuts/ShortcutsProvider";

const densityKey = "studio.density";
const densities: readonly Density[] = ["compact", "normal", "comfortable"];

function storedDensity(): Density {
  try {
    const saved = window.localStorage.getItem(densityKey);
    return densities.find((d) => d === saved) ?? "normal";
  } catch {
    return "normal";
  }
}

function saveDensity(density: Density): void {
  try {
    window.localStorage.setItem(densityKey, density);
  } catch {
    // Storage may be blocked; the choice then lasts until reload.
  }
}

function Structure({ detail }: { detail: TableDetail }): ReactElement {
  return (
    <details className="structure">
      <summary>Columns</summary>
      <table className="data-table" aria-label="Columns">
        <thead>
          <tr>
            <th scope="col">Column</th>
            <th scope="col">Type</th>
            <th scope="col">Null</th>
            <th scope="col">Default</th>
            <th scope="col">Key</th>
          </tr>
        </thead>
        <tbody>
          {detail.columns.map((c) => (
            <tr key={c.name}>
              <th scope="row" className="mono">
                {c.name}
              </th>
              <td className="mono">{c.type}</td>
              <td>{c.nullable ? "yes" : "no"}</td>
              <td className="mono">{c.default ?? ""}</td>
              <td>{detail.primaryKey.includes(c.name) ? "primary" : ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}

function TableData({
  source,
  table,
  detail,
  tables,
}: {
  source: string;
  table: string;
  detail: TableDetail;
  tables: readonly TableSummary[];
}): ReactElement {
  const search = useSearch();
  const { view, rejected } = useMemo(() => searchToView(search), [search]);
  const open = openFromSearch(search);
  const viewSearch = viewToSearch(view);
  const [density, setDensity] = useState<Density>(storedDensity);
  const [cellsTarget, setCellsTarget] = useState<DrawerTarget | null>(null);
  const estimate = tables.find((t) => t.name === table)?.rowEstimate ?? -1;

  const data = useGridData({
    source,
    table,
    view,
    hasPrimaryKey: detail.primaryKey.length > 0,
    estimate,
  });
  const { message, announce } = useAnnouncer();
  const filterValue = useRef<HTMLInputElement>(null);
  const [filterRequest, setFilterRequest] = useState<FilterRequest | null>(null);

  useShortcut({
    key: "/",
    description: "Filter: go to the filter bar",
    run: () => (filterValue.current ?? document.getElementById("filter-column"))?.focus(),
  });
  useShortcut({
    key: "r",
    description: "Refresh the rows on screen",
    run: () => {
      data.refresh();
      announce("Refreshing the rows on screen.");
    },
  });

  // The open row lives in the address so it can be linked; changing the view keeps it.
  const setView = (next: ViewState): void => replaceSearch(viewToSearch(next, open));

  const target: DrawerTarget | null = open ? { kind: "key", key: unpackKey(open) } : cellsTarget;

  const openRow = (index: number): void => {
    const cells = data.row(index);
    if (!cells) {
      announce("That row is not loaded yet.");
      return;
    }
    const parts = keyParts(detail.columns, detail.primaryKey, cells);
    if (parts) {
      replaceSearch(viewToSearch(view, packKey(parts)));
    } else {
      setCellsTarget({ kind: "cells", cells, number: index + 1 });
    }
  };

  const closeRow = (): void => {
    setCellsTarget(null);
    if (open) {
      replaceSearch(viewSearch);
    }
  };

  const linkFor = (column: string, cells: readonly ApiCell[]): RowLink | null => {
    const fk = detail.foreignKeys.find((f) => f.columns.includes(column));
    return fk ? foreignKeyLink(fk, detail.columns, cells, tables) : null;
  };

  return (
    <>
      <div className="table-toolbar">
        <label className="field-inline">
          <span>Row height</span>
          <select
            value={density}
            onChange={(e) => {
              const next = densities.find((d) => d === e.target.value) ?? "normal";
              setDensity(next);
              saveDensity(next);
            }}
          >
            <option value="compact">Compact</option>
            <option value="normal">Normal</option>
            <option value="comfortable">Comfortable</option>
          </select>
        </label>
      </div>
      <FilterBar columns={detail.columns} view={view} onChange={setView} valueRef={filterValue} request={filterRequest} />
      {rejected > 0 ? (
        <p className="notice" role="status">
          {rejected} filter{rejected === 1 ? " in the address was" : "s in the address were"} not understood and not
          applied.
        </p>
      ) : null}
      {data.error ? <ErrorNotice error={data.error} onRetry={data.retry} /> : null}
      <Grid
        source={source}
        label={`${table} rows`}
        columns={detail.columns}
        primaryKey={detail.primaryKey}
        data={data}
        view={view}
        density={density}
        announce={announce}
        resetKey={viewSearch}
        onOpenRow={openRow}
        linkFor={linkFor}
        onSort={(column) => setView(nextSort(view, column))}
        onSetSort={(column, desc) => setView({ ...view, sort: column, desc: column !== null && desc })}
        onFilterColumn={(column) => setFilterRequest((prev) => ({ column, nonce: (prev?.nonce ?? 0) + 1 }))}
      />
      <RowDrawer source={source} table={table} detail={detail} tables={tables} target={target} onClose={closeRow} />
      <LiveRegion message={message} />
    </>
  );
}

/** TablePage shows one table as a data grid, with its structure folded under the header. */
export function TablePage({ source, table }: { source: string; table: string }): ReactElement {
  const detail = useApi<TableDetail>(apiPath("sources", source, "tables", table));
  const tables = useApi<TablesResponse>(apiPath("sources", source, "tables"));

  return (
    <section className="table-page" aria-labelledby="table-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="table-title">{table}</h1>

      {detail.loading || tables.loading ? <Loading what="table" /> : null}
      {detail.error ? <ErrorNotice error={detail.error} onRetry={detail.reload} /> : null}
      {tables.error ? <ErrorNotice error={tables.error} onRetry={tables.reload} /> : null}

      {detail.data && tables.data ? (
        <>
          <Structure detail={detail.data} />
          <TableData source={source} table={table} detail={detail.data} tables={tables.data.tables} />
        </>
      ) : null}
    </section>
  );
}
