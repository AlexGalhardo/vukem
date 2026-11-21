import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { CollectionsResponse, Source, TablesResponse } from "../api/types";
import { useApi } from "../api/useApi";
import { EmptyState, ErrorNotice, Loading } from "../components/Notices";
import { Link, navigate } from "../lib/history";
import { formatCount } from "../lib/mode";
import { hrefFor } from "../lib/router";
import { KeyBrowser } from "../redis/KeyBrowser";

function TableList({ source }: { source: Source }): ReactElement {
  const { data, error, loading, reload } = useApi<TablesResponse>(apiPath("sources", source.name, "tables"));

  if (loading) return <Loading what="tables" />;
  if (error) return <ErrorNotice error={error} onRetry={reload} />;
  if (!data || data.tables.length === 0) return <EmptyState title="This database has no tables" />;

  return (
    <table className="data-table" aria-label="Tables">
      <thead>
        <tr>
          <th scope="col">Table</th>
          <th scope="col" className="num">
            Rows (estimate)
          </th>
          <th scope="col">Primary key</th>
        </tr>
      </thead>
      <tbody>
        {data.tables.map((t) => (
          <tr key={t.name}>
            <th scope="row">
              <Link route={{ name: "table", source: source.name, table: t.name }}>{t.name}</Link>
            </th>
            <td className="num">{formatCount(t.rowEstimate)}</td>
            <td className="mono">{t.primaryKey.length > 0 ? t.primaryKey.join(", ") : "none"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function CollectionList({ source }: { source: Source }): ReactElement {
  const { data, error, loading, reload } = useApi<CollectionsResponse>(apiPath("sources", source.name, "collections"));

  if (loading) return <Loading what="collections" />;
  if (error) return <ErrorNotice error={error} onRetry={reload} />;
  if (!data || data.collections.length === 0) return <EmptyState title="This database has no collections" />;

  return (
    <table className="data-table" aria-label="Collections">
      <thead>
        <tr>
          <th scope="col">Collection</th>
          <th scope="col" className="num">
            Documents
          </th>
        </tr>
      </thead>
      <tbody>
        {data.collections.map((c) => (
          <tr key={c.name}>
            <th scope="row">
              <Link route={{ name: "collection", source: source.name, collection: c.name }}>{c.name}</Link>
            </th>
            <td className="num">{c.estimate < 0 ? "unknown" : `about ${formatCount(c.estimate)}`}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** SourcePage lists the tables, collections or keys of one source. */
export function SourcePage({ source }: { source: Source }): ReactElement {
  return (
    <section aria-labelledby="source-title">
      <h1 id="source-title">{source.name}</h1>
      <p className="muted mono">{source.label}</p>
      <p>
        <Link route={{ name: "console", source: source.name }}>{source.kind === "sql" ? "Open the SQL console" : "Open the console"}</Link>
        {source.kind === "sql" ? (
          <>
            {" · "}
            <Link route={{ name: "schema", source: source.name }}>Open the schema</Link>
          </>
        ) : null}
      </p>
      {source.kind === "sql" ? <TableList source={source} /> : null}
      {source.kind === "mongo" ? <CollectionList source={source} /> : null}
      {source.kind === "redis" ? <KeyBrowser source={source.name} onOpenKey={(key) => navigate(hrefFor({ name: "key", source: source.name, key }))} /> : null}
    </section>
  );
}
