import { useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiGet, apiPath } from "../api/client";
import type { ApiCell, CollectionsResponse } from "../api/types";
import { useApi } from "../api/useApi";
import { PlusIcon, RefreshIcon } from "../components/icons";
import { ErrorNotice } from "../components/Notices";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { emptyView, formatFilter, nextSort } from "../grid/filters";
import type { ViewState } from "../grid/filters";
import { Grid } from "../grid/Grid";
import type { Chunk } from "../grid/stream";
import { useStreamed } from "../grid/useStreamed";
import { canUseField, columnsOf, compact, inferColumns, parseDoc, rowOf } from "./docs";
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

const pageSize = 100;
const maxSkipPages = 1000;
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
 * CollectionView browses one MongoDB collection: a virtualized list of
 * documents in a Table view (columns from the first 100 documents) or a JSON
 * view, a filter bar, a sort, and a drawer for one document. Documents load as
 * you scroll: by _id cursor in the default order, by page when sorted by a field.
 */
export function CollectionView({ source, collection, write }: { source: string; collection: string; write: boolean }): ReactElement {
  const [view, setView] = useState<ViewState>(emptyView);
  const [mode, setMode] = useState<Mode>("table");
  const [columns, setColumns] = useState<string[]>([]);
  const [openId, setOpenId] = useState<string | null>(null);
  const [insertOpen, setInsertOpen] = useState(false);
  const [nonce, setNonce] = useState(0);
  const { message, announce } = useAnnouncer();
  const entries = useRef(new WeakMap<ApiCell[], Entry>());
  const columnsRef = useRef<string[]>([]);
  const list = useApi<CollectionsResponse>(apiPath("sources", source, "collections"));
  const estimate = list.data?.collections.find((c) => c.name === collection)?.estimate ?? -1;

  const keyset = view.sort === null || view.sort === "_id";

  const streamed = useStreamed({
    resetKey: `${source}|${collection}|${queryFor(view, {})}|${nonce}`,
    auto: true,
    fetchChunk: async (cursor, step, signal): Promise<Chunk> => {
      const extra: Record<string, string> = { perPage: String(pageSize) };
      if (keyset && cursor !== null) {
        extra.after = cursor;
      }
      if (!keyset) {
        extra.page = String(step + 1);
      }
      const res = await apiGet<DocsResponse>(
        apiPath("sources", source, "collections", collection, "documents") + queryFor(view, extra),
        signal,
      );
      const docs = res.docs.map((d) => ({ ...d, doc: parseDoc(d.json) }));
      if (step === 0) {
        columnsRef.current = inferColumns(docs.map((d) => d.doc));
        setColumns(columnsRef.current);
      }
      const rows = docs.map((d) => {
        const cells = rowOf(columnsRef.current, d.doc);
        entries.current.set(cells, { id: d.id, summary: summaryOf(d.json) });
        return cells;
      });
      const next = keyset ? (res.next ?? null) : res.hasMore && step + 2 <= maxSkipPages ? String(step + 2) : null;
      return { rows, next, hasMore: res.hasMore && next !== null };
    },
  });

  const data = {
    ...streamed.data,
    row: (index: number): ApiCell[] | undefined => {
      const r = streamed.data.row(index);
      return r && mode === "json" ? entries.current.get(r)?.summary : r;
    },
  };

  const tableColumns = useMemo(() => columnsOf(columns), [columns]);
  const sortable = columns.filter(canUseField);
  const cappedByPages = !keyset && !streamed.hasMore && streamed.loaded >= maxSkipPages * pageSize;

  const open = (index: number): void => {
    const r = streamed.data.row(index);
    const entry = r ? entries.current.get(r) : undefined;
    if (entry) {
      setOpenId(entry.id);
    } else {
      announce("That document is not loaded yet.");
    }
  };

  const reload = (): void => setNonce((n) => n + 1);

  return (
    <section className="table-page doc-view" aria-label={`Collection ${collection}`}>
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
        <button type="button" className="button button-small button-with-icon" onClick={reload}>
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
          Sorted by {view.sort}: MongoDB reads page by page from the start of the sort, which gets slower the deeper you go, so Studio stops at page {maxSkipPages} ({(maxSkipPages * pageSize).toLocaleString("en-US")} documents). Sorting by _id has no such limit.
        </p>
      ) : null}

      <DocFilterBar fields={columns} view={view} onChange={setView} />

      {streamed.data.error ? <ErrorNotice error={streamed.data.error} onRetry={streamed.data.retry} /> : null}
      <p className="muted" role="status">
        {streamed.loaded.toLocaleString("en-US")} {streamed.loaded === 1 ? "document" : "documents"} loaded
        {streamed.hasMore ? "; more load as you scroll" : ""}
        {estimate >= 0 ? `. The collection holds about ${estimate.toLocaleString("en-US")}.` : "."}
        {cappedByPages ? " The page limit was reached." : ""}
      </p>

      <Grid
        source=""
        label={`${collection} documents`}
        columns={mode === "json" ? jsonColumns : tableColumns}
        primaryKey={[]}
        data={data}
        view={mode === "json" ? emptyView : view}
        density="normal"
        announce={announce}
        resetKey={`${mode}|${queryFor(view, {})}|${nonce}`}
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
          reload();
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
            reload();
          }}
        />
      ) : null}
      <LiveRegion message={message} />
    </section>
  );
}
