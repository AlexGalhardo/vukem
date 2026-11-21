import { useMemo, useState } from "react";
import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { TableDetail, TablesResponse } from "../api/types";
import { useApi } from "../api/useApi";
import { ErrorNotice, Loading } from "../components/Notices";
import { Grid } from "../grid/Grid";
import { nextSort, searchToView, viewToSearch } from "../grid/filters";
import { useGridData } from "../grid/useGridData";
import type { Density } from "../grid/virtual";
import { Link, replaceSearch, useSearch } from "../lib/history";

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
  estimate,
}: {
  source: string;
  table: string;
  detail: TableDetail;
  estimate: number;
}): ReactElement {
  const search = useSearch();
  const { view, rejected } = useMemo(() => searchToView(search), [search]);
  const [density, setDensity] = useState<Density>(storedDensity);

  const data = useGridData({
    source,
    table,
    view,
    hasPrimaryKey: detail.primaryKey.length > 0,
    estimate,
    tail: false,
  });

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
      {rejected > 0 ? (
        <p className="notice" role="status">
          {rejected} filter{rejected === 1 ? " in the address was" : "s in the address were"} not understood and not
          applied.
        </p>
      ) : null}
      {data.error ? <ErrorNotice error={data.error} onRetry={data.retry} /> : null}
      <Grid
        label={`${table} rows`}
        columns={detail.columns}
        primaryKey={detail.primaryKey}
        data={data}
        view={view}
        density={density}
        onSort={(column) => replaceSearch(viewToSearch(nextSort(view, column)))}
      />
    </>
  );
}

/** TablePage shows one table as a data grid, with its structure folded under the header. */
export function TablePage({ source, table }: { source: string; table: string }): ReactElement {
  const detail = useApi<TableDetail>(apiPath("sources", source, "tables", table));
  const tables = useApi<TablesResponse>(apiPath("sources", source, "tables"));
  const estimate = tables.data?.tables.find((t) => t.name === table)?.rowEstimate ?? -1;

  return (
    <section className="table-page" aria-labelledby="table-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="table-title">{table}</h1>

      {detail.loading || tables.loading ? <Loading what="table" /> : null}
      {detail.error ? <ErrorNotice error={detail.error} onRetry={detail.reload} /> : null}

      {detail.data && !tables.loading ? (
        <>
          <Structure detail={detail.data} />
          <TableData source={source} table={table} detail={detail.data} estimate={estimate} />
        </>
      ) : null}
    </section>
  );
}
