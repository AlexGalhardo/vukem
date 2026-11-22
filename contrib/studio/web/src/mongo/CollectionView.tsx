import { useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiGet, apiPath } from "../api/client";
import type { ApiCell, CollectionsResponse, CountResponse } from "../api/types";
import { useApi } from "../api/useApi";
import { PlusIcon, RefreshIcon } from "../components/icons";
import { ErrorNotice } from "../components/Notices";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { emptyView, formatFilter, nextSort } from "../grid/filters";
import type { ViewState } from "../grid/filters";
import { Grid } from "../grid/Grid";
import { orderedRowCap, sortedRowCap } from "../grid/paging";
import { usePagedRows } from "../grid/usePagedRows";
import { canUseField, columnsOf, compact, inferColumns, maxColumns, parseDoc, rowOf } from "./docs";
import { DocFilterBar } from "./DocFilterBar";
import { DocumentDrawer } from "./DocumentDrawer";
import { InsertDialog } from "./InsertDialog";

interface DocsResponse {
  docs: { id: string; json: string }[];
  next?: string;
  hasMore: boolean;
}

interface Entry {
  id: string;
  summary: ApiCell[];
}

type Mode = "table" | "json";

const jsonColumns = columnsOf(["document"]);
const cutAt = 300;

function summaryOf(json: string): ApiCell[] {
  const doc = parseDoc(json);
  const text = doc ? compact(doc) : json;
  return [text.length > cutAt ? { t: text.slice(0, cutAt), x: true } : { t: text }];
}

/** The download of the documents the view shows: its filters, at most 100,000 documents. */
function exportHref(source: string, collection: string, view: ViewState): string {
  const params = new URLSearchParams();
  for (const f of view.filters) {
    params.append("filter", formatFilter(f));
  }
  if (view.desc) {
    params.set("desc", "1");
  }
  const q = params.toString();
  return `/api/v1${apiPath("sources", source, "collections", collection, "export")}${q === "" ? "" : `?${q}`}`;
}

function queryFor(view: ViewState, extra: Record<string, string>): string {
  const params = new URLSearchParams();
  for (const f of view.filters) {
    params.append("filter", formatFilter(f));
  }
  if (view.sort !== null) {
    params.set("sort", view.sort);
  }
  if (view.desc) {
    params.set("desc", "1");
  }
  for (const [k, v] of Object.entries(extra)) {
    params.set(k, v);
  }
  return `?${params.toString()}`;
}

/**
 * CollectionView browses one MongoDB collection: documents a page at a time in
 * a Table view (columns from the documents seen so far) or a JSON view, a
 * filter bar, a sort, and a drawer for one document. Only the current page is
 * fetched; the exact count comes apart, so the last page is known.
 */
export function CollectionView({ source, collection, write }: { source: string; collection: string; write: boolean }): ReactElement {
  const [view, setView] = useState<ViewState>(emptyView);
  const [mode, setMode] = useState<Mode>("table");
  const [columns, setColumns] = useState<string[]>([]);
  const [openId, setOpenId] = useState<string | null>(null);
  const [insertOpen, setInsertOpen] = useState(false);
  const { message, announce } = useAnnouncer();
  const entries = useRef(new WeakMap<ApiCell[], Entry>());
  const columnsRef = useRef<{ key: string; names: string[] }>({ key: "", names: [] });
  const list = useApi<CollectionsResponse>(apiPath("sources", source, "collections"));
  const estimate = list.data?.collections.find((c) => c.name === collection)?.estimate ?? -1;

  const keyset = view.sort === null || view.sort === "_id";
  const viewKey = `${source}|${collection}|${queryFor(view, {})}`;

  const paged = usePagedRows({
    resetKey: viewKey,
    rowCap: keyset ? orderedRowCap : sortedRowCap,
    estimate: view.filters.length === 0 ? estimate : -1,
    fetchPage: async (page, size, signal) => {
      const res = await apiGet<DocsResponse>(
        apiPath("sources", source, "collections", collection, "documents") +
          queryFor(view, { perPage: String(size), page: String(page) }),
        signal,
      );
      const docs = res.docs.map((d) => ({ ...d, doc: parseDoc(d.json) }));

      // The columns are the fields seen so far in this view: a later page can add some, never drop one.
      const seen = columnsRef.current.key === viewKey ? columnsRef.current.names : [];
      const names = [...seen, ...inferColumns(docs.map((d) => d.doc)).filter((c) => !seen.includes(c))].slice(0, maxColumns);
      if (names.length !== seen.length || columnsRef.current.key !== viewKey) {
        columnsRef.current = { key: viewKey, names };
        setColumns(names);
      }

      const rows = docs.map((d) => {
        const cells = rowOf(names, d.doc);
        entries.current.set(cells, { id: d.id, summary: summaryOf(d.json) });
        return cells;
      });
      return { rows, hasMore: res.hasMore };
    },
    fetchTotal: async (signal) => {
      const res = await apiGet<CountResponse>(
        apiPath("sources", source, "collections", collection, "count") + queryFor(view, {}),
        signal,
      );
      return res.count ?? null;
    },
  });

  const data = {
    ...paged,
    row: (index: number): ApiCell[] | undefined => {
      const r = paged.row(index);
      return r && mode === "json" ? entries.current.get(r)?.summary : r;
    },
  };

  const tableColumns = useMemo(() => columnsOf(columns), [columns]);
  const sortable = columns.filter(canUseField);

  const open = (index: number): void => {
    const r = paged.row(index);
    const entry = r ? entries.current.get(r) : undefined;
    if (entry) {
      setOpenId(entry.id);
    } else {
      announce("That document is not loaded yet.");
    }
  };

  return (
    <section className="table-page table-page-paged doc-view" aria-label={`Collection ${collection}`}>
      <div className="table-toolbar">
        <div role="group" aria-label="View" className="toggle-group">
          <button type="button" className="button button-small" aria-pressed={mode === "table"} onClick={() => setMode("table")}>
            Table
          </button>
          <button type="button" className="button button-small" aria-pressed={mode === "json"} onClick={() => setMode("json")}>
            JSON
          </button>
        </div>
        <label className="field-inline">
          <span>Sort by</span>
          <select
            value={view.sort ?? "_id"}
            onChange={(e) => setView({ ...view, sort: e.target.value === "_id" ? null : e.target.value })}
          >
            <option value="_id">_id (fastest)</option>
            {sortable
              .filter((c) => c !== "_id")
              .map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
          </select>
        </label>
        <label className="field-inline">
          <span>Direction</span>
          <select value={view.desc ? "desc" : "asc"} onChange={(e) => setView({ ...view, desc: e.target.value === "desc" })}>
            <option value="asc">Ascending</option>
            <option value="desc">Descending</option>
          </select>
        </label>
        <button type="button" className="button button-small button-with-icon" onClick={paged.refresh}>
          <RefreshIcon />
          Refresh
        </button>
        <a className="button button-small" href={exportHref(source, collection, view)} download>
          Export JSON Lines
        </a>
        {write ? (
          <button type="button" className="button button-small button-with-icon" onClick={() => setInsertOpen(true)}>
            <PlusIcon />
            Insert document
          </button>
        ) : null}
      </div>

      {!keyset ? (
        <p className="notice" role="note">
          Sorted by {view.sort}: MongoDB reads page by page from the start of the sort, which gets slower the deeper you go, so Studio pages through the first {sortedRowCap.toLocaleString("en-US")} documents. Sorting by _id reaches {orderedRowCap.toLocaleString("en-US")}.
        </p>
      ) : null}

      <DocFilterBar fields={columns} view={view} onChange={setView} />

      {paged.error ? <ErrorNotice error={paged.error} onRetry={paged.retry} /> : null}

      <Grid
        source=""
        label={`${collection} documents`}
        columns={mode === "json" ? jsonColumns : tableColumns}
        primaryKey={[]}
        data={data}
        view={mode === "json" ? emptyView : view}
        density="normal"
        announce={announce}
        resetKey={`${mode}|${queryFor(view, {})}|${paged.paging?.page}|${paged.paging?.size}`}
        onOpenRow={open}
        onSort={(column) => {
          if (mode === "table" && canUseField(column)) {
            setView(nextSort(view, column));
          } else {
            announce(`${column} is not a field path Studio can sort by.`);
          }
        }}
        onSetSort={(column, desc) => {
          if (column === null || canUseField(column)) {
            setView({ ...view, sort: column, desc: column !== null && desc });
          }
        }}
      />

      <DocumentDrawer
        source={source}
        collection={collection}
        id={openId}
        write={write}
        onClose={() => setOpenId(null)}
        onChanged={(what) => {
          announce(`Document ${what}.`);
          paged.reload();
        }}
      />
      {write ? (
        <InsertDialog
          source={source}
          collection={collection}
          open={insertOpen}
          onClose={() => setInsertOpen(false)}
          onDone={() => {
            setInsertOpen(false);
            announce("Document inserted.");
            paged.reload();
          }}
        />
      ) : null}
      <LiveRegion message={message} />
    </section>
  );
}
