import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { TableDetail } from "../api/types";
import { useApi } from "../api/useApi";
import { ErrorNotice, Loading } from "../components/Notices";
import { Link } from "../lib/history";

/** TablePage shows the structure of one table; the data grid comes in a later release. */
export function TablePage({ source, table }: { source: string; table: string }): ReactElement {
  const { data, error, loading, reload } = useApi<TableDetail>(apiPath("sources", source, "tables", table));

  return (
    <section aria-labelledby="table-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="table-title">{table}</h1>

      {loading ? <Loading what="columns" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {data ? (
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
            {data.columns.map((c) => (
              <tr key={c.name}>
                <th scope="row" className="mono">
                  {c.name}
                </th>
                <td className="mono">{c.type}</td>
                <td>{c.nullable ? "yes" : "no"}</td>
                <td className="mono">{c.default ?? ""}</td>
                <td>{data.primaryKey.includes(c.name) ? "primary" : ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </section>
  );
}
