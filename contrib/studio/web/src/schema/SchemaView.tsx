import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiGet, apiPath } from "../api/client";
import type { CreateResponse, SchemaResponse, SchemaTable } from "../api/types";
import { useApi } from "../api/useApi";
import { EmptyState, ErrorNotice, Loading } from "../components/Notices";
import { copyText } from "../grid/clipboard";
import { Link, navigate } from "../lib/history";
import { hrefFor } from "../lib/router";
import { Diagram } from "./Diagram";
import { layoutSchema } from "./layout";
import { diagramTables, keyRoles, layoutInputs, matchesFilter, maxDiagramTables, referencedBy, relations } from "./model";
import { RelationsTable } from "./RelationsTable";

export interface SchemaViewProps {
  source: string;
  /** Opens a table in a tab. Without it the table opens as its own page. */
  onOpenTable?: (table: string) => void;
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

function CreateStatement({ source, table }: { source: string; table: string }): ReactElement {
  const [state, setState] = useState<{ table: string; text: string } | { table: string; error: string } | null>(null);
  const [copied, setCopied] = useState("");
  const shown = state !== null && state.table === table ? state : null;

  const show = (): void => {
    apiGet<CreateResponse>(apiPath("sources", source, "tables", table, "create")).then(
      (res) => setState({ table, text: res.statement }),
      () => setState({ table, error: "The database did not report a CREATE statement for this table." }),
    );
  };

  return (
    <div className="schema-create">
      {shown && "text" in shown ? (
        <>
          <div className="schema-create-bar">
            <h3>CREATE statement</h3>
            <button
              type="button"
              className="button button-small"
              onClick={() => void copyText(shown.text).then((ok) => setCopied(ok ? "Copied." : "The browser did not allow copying."))}
            >
              Copy
            </button>
            <button type="button" className="button button-small" onClick={() => setState(null)}>
              Hide
            </button>
            <span role="status" className="muted">
              {copied}
            </span>
          </div>
          <pre className="plan" tabIndex={0} aria-label={`CREATE statement of ${table}`}>
            {shown.text}
          </pre>
        </>
      ) : (
        <>
          <button type="button" className="button button-small" onClick={show}>
            Show CREATE statement
          </button>
          {shown && "error" in shown ? (
            <p role="alert" className="muted">
              {shown.error}
            </p>
          ) : null}
        </>
      )}
    </div>
  );
}

function TableDetail({
  table,
  all,
  source,
  canShowCreate,
  onSelect,
  onOpen,
}: {
  table: SchemaTable;
  all: readonly ReturnType<typeof relations>[number][];
  source: string;
  canShowCreate: boolean;
  onSelect: (table: string) => void;
  onOpen: (table: string) => void;
}): ReactElement {
  const incoming = referencedBy(all, table.name);

  return (
    <section className="schema-detail" aria-labelledby="schema-detail-title">
      <div className="schema-detail-head">
        <h2 id="schema-detail-title" className="mono">
          {table.name}
        </h2>
        <button type="button" className="button button-small" onClick={() => onOpen(table.name)}>
          Open table
        </button>
      </div>

      <h3>Columns</h3>
      <table className="data-table" aria-label={`Columns of ${table.name}`}>
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
          {table.columns.map((c) => (
            <tr key={c.name}>
              <th scope="row" className="mono">
                {c.name}
              </th>
              <td className="mono">{c.type}</td>
              <td>{c.nullable ? "yes" : "no"}</td>
              <td className="mono">{c.default ?? ""}</td>
              <td>
                {keyRoles(table, c.name).map((r) => (
                  <span key={r} className={`badge key-badge key-${r}`}>
                    {r}
                  </span>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3>Indexes</h3>
      {table.indexes.length === 0 ? (
        <p className="muted">No index.</p>
      ) : (
        <table className="data-table" aria-label={`Indexes of ${table.name}`}>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Columns, in order</th>
              <th scope="col">Kind</th>
            </tr>
          </thead>
          <tbody>
            {table.indexes.map((ix, i) => (
              <tr key={`${ix.name}:${i}`}>
                <th scope="row" className="mono">
                  {ix.primary ? "primary key" : ix.name === "" ? "(unnamed)" : ix.name}
                </th>
                <td className="mono">
                  {ix.columns.join(", ")}
                  {ix.expression ? " + an expression" : ""}
                </td>
                <td>{[ix.primary ? "primary" : "", ix.unique && !ix.primary ? "unique" : "", ix.columns.length > 1 ? "composite" : ""].filter(Boolean).join(", ") || "index"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h3>Foreign keys</h3>
      {table.foreignKeys.length === 0 ? (
        <p className="muted">This table points at no other table.</p>
      ) : (
        <ul className="schema-links">
          {table.foreignKeys.map((fk, i) => (
            <li key={`${fk.refTable}:${i}`}>
              <span className="mono">{fk.columns.join(", ")}</span> points at{" "}
              <button type="button" className="link-button" onClick={() => onSelect(fk.refTable)}>
                {fk.refTable}
              </button>{" "}
              (<span className="mono">{fk.refColumns.join(", ")}</span>)
            </li>
          ))}
        </ul>
      )}

      <h3>Referenced by</h3>
      {incoming.length === 0 ? (
        <p className="muted">No table points at this one.</p>
      ) : (
        <ul className="schema-links">
          {incoming.map((r, i) => (
            <li key={`${r.fromTable}:${i}`}>
              <button type="button" className="link-button" onClick={() => onSelect(r.fromTable)}>
                {r.fromTable}
              </button>{" "}
              (<span className="mono">{r.fromColumns.join(", ")}</span>) points here at <span className="mono">{r.toColumns.join(", ")}</span>
            </li>
          ))}
        </ul>
      )}

      {canShowCreate ? <CreateStatement source={source} table={table.name} /> : null}
    </section>
  );
}

/**
 * SchemaView is the schema of a SQL source: a filterable table list, the
 * detail of the selected table, a relationship diagram and the same
 * relationships as a table.
 */
export function SchemaView({ source, onOpenTable }: SchemaViewProps): ReactElement {
  const { data, error, loading, reload } = useApi<SchemaResponse>(apiPath("sources", source, "schema"));
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [showDiagram, setShowDiagram] = useState(true);
  const [focusOnly, setFocusOnly] = useState(false);
  const first = useRef(true);

  const tables = data?.tables ?? [];
  const all = useMemo(() => relations(tables), [tables]);
  const current = tables.find((t) => t.name === selected) ?? null;

  useEffect(() => {
    // The first table is selected once, so the detail pane is never empty for a schema that has tables.
    if (first.current && tables.length > 0) {
      first.current = false;
      setSelected(tables[0]?.name ?? null);
    }
  }, [tables]);

  const drawn = useMemo(() => diagramTables(tables, selected, focusOnly), [tables, selected, focusOnly]);
  const layout = useMemo(() => layoutSchema(layoutInputs(drawn.tables)), [drawn.tables]);
  const subtitles = useMemo(
    () => new Map(tables.map((t) => [t.name, `${plural(t.columns.length, "column", "columns")}`])),
    [tables],
  );

  if (loading) return <Loading what="the schema" />;
  if (error) return <ErrorNotice error={error} onRetry={reload} />;
  if (!data || tables.length === 0) return <EmptyState title="This database has no tables" />;

  const open = (table: string): void => (onOpenTable ? onOpenTable(table) : navigate(hrefFor({ name: "table", source, table })));
  const visible = tables.filter((t) => matchesFilter(t.name, filter));
  const description = `Diagram of ${plural(drawn.tables.length, "table", "tables")} and ${plural(layout.edges.length, "foreign key", "foreign keys")}. Every table is a link that opens it; the same relationships are listed in the Relationships table below.`;

  return (
    <div className="schema">
      <nav className="schema-list" aria-label="Tables of the schema">
        <label className="field-inline" htmlFor="schema-filter">
          <span>Filter tables</span>
        </label>
        <input
          id="schema-filter"
          type="search"
          value={filter}
          autoComplete="off"
          spellCheck={false}
          placeholder="Filter tables"
          onChange={(e) => setFilter(e.target.value)}
        />
        <p className="muted" role="status">
          {visible.length === tables.length ? plural(tables.length, "table", "tables") : `${visible.length} of ${tables.length} tables`}
        </p>
        <ul className="schema-tables">
          {visible.map((t) => (
            <li key={t.name}>
              <button
                type="button"
                className="schema-table-button"
                aria-current={t.name === selected ? "true" : undefined}
                onClick={() => setSelected(t.name)}
              >
                <span className="truncate mono">{t.name}</span>
                <span className="muted">{t.columns.length}</span>
              </button>
            </li>
          ))}
        </ul>
      </nav>

      <div className="schema-main">
        {current ? (
          <TableDetail
            table={current}
            all={all}
            source={source}
            canShowCreate={data.canShowCreate}
            onSelect={setSelected}
            onOpen={open}
          />
        ) : (
          <p className="muted">Pick a table to see its columns, indexes and relationships.</p>
        )}

        <section className="schema-diagram" aria-labelledby="schema-diagram-title">
          <div className="schema-diagram-head">
            <h2 id="schema-diagram-title">Relationship diagram</h2>
            <label className="field-inline">
              <input type="checkbox" role="switch" checked={showDiagram} onChange={(e) => setShowDiagram(e.target.checked)} />
              <span>Show the diagram</span>
            </label>
            {showDiagram ? (
              <label className="field-inline">
                <input type="checkbox" role="switch" checked={focusOnly} disabled={selected === null} onChange={(e) => setFocusOnly(e.target.checked)} />
                <span>Only the selected table and its neighbors</span>
              </label>
            ) : null}
          </div>
          {showDiagram ? (
            <>
              {drawn.cut ? (
                <p className="notice" role="status">
                  The diagram draws the first {maxDiagramTables} tables. Select one and use the neighbors switch to see the rest.
                </p>
              ) : null}
              <Diagram
                layout={layout}
                subtitles={subtitles}
                selected={selected}
                onSelect={setSelected}
                onOpen={open}
                description={description}
              />
            </>
          ) : null}
        </section>

        <section className="schema-relations" aria-labelledby="schema-relations-title">
          <h2 id="schema-relations-title">Relationships</h2>
          <RelationsTable relations={all} selected={selected} onOpen={setSelected} />
        </section>
      </div>
    </div>
  );
}

/** SchemaPage is the schema as a page of its own. */
export function SchemaPage({ source }: { source: string }): ReactElement {
  return (
    <section aria-labelledby="schema-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="schema-title">Schema</h1>
      <SchemaView source={source} />
    </section>
  );
}
