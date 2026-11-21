import { useMemo, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import { apiGet, apiPath } from "../api/client";
import type { ApiCell, Column, KeysResponse } from "../api/types";
import { ErrorNotice } from "../components/Notices";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { emptyView } from "../grid/filters";
import type { ViewState } from "../grid/filters";
import { nextSort } from "../grid/filters";
import { Grid } from "../grid/Grid";
import { useStreamed } from "../grid/useStreamed";
import { sortRows } from "../console/resultData";
import { formatTTL } from "../lib/mode";

const columns: Column[] = [
  { name: "Key", type: "", nullable: false },
  { name: "Type", type: "", nullable: false },
  { name: "Expires in", type: "", nullable: false },
];

const counts = [100, 200, 500, 1000] as const;

function keyRow(k: { key: string; type: string; ttlSeconds: number }): ApiCell[] {
  return [{ t: k.key }, { t: k.type }, { t: formatTTL(k.ttlSeconds) }];
}

/**
 * KeyBrowser lists keys with SCAN, one step at a time. SCAN is incremental:
 * a step returns some keys and a cursor, and the scan ends when the cursor is
 * 0. It can return a key twice (shown once here) and can miss keys that were
 * created while it ran. Nothing here uses KEYS, which would block the server.
 */
export function KeyBrowser({ source, onOpenKey }: { source: string; onOpenKey: (key: string) => void }): ReactElement {
  const [pattern, setPattern] = useState("");
  const [count, setCount] = useState<number>(200);
  const [applied, setApplied] = useState({ pattern: "", count: 200, nonce: 0 });
  const [view, setView] = useState<ViewState>(emptyView);
  const { message, announce } = useAnnouncer();

  const streamed = useStreamed({
    resetKey: `${source}|${applied.pattern}|${applied.count}|${applied.nonce}`,
    auto: false,
    keyOf: (row) => row[0]?.t,
    fetchChunk: async (cursor, _step, signal) => {
      const params = new URLSearchParams({ count: String(applied.count) });
      if (applied.pattern !== "") params.set("match", applied.pattern);
      if (cursor !== null) params.set("cursor", cursor);
      const res = await apiGet<KeysResponse>(apiPath("sources", source, "keys") + `?${params.toString()}`, signal);
      const next = res.cursor ?? null;
      return { rows: res.keys.map(keyRow), next, hasMore: next !== null };
    },
  });

  const sortIndex = view.sort === null ? null : columns.findIndex((c) => c.name === view.sort);
  const rows = useMemo(
    () => sortRows(streamed.rows, sortIndex, view.desc),
    // The rows array is the same object while it grows: the version says when it changed.
    [streamed.rows, streamed.data.version, sortIndex, view.desc],
  );
  const data = { ...streamed.data, row: (i: number): ApiCell[] | undefined => rows[i] as ApiCell[] | undefined };

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    setApplied((a) => ({ pattern: pattern.trim(), count, nonce: a.nonce + 1 }));
  };

  return (
    <section className="key-browser" aria-label="Keys">
      <form className="filter-form" onSubmit={submit} aria-label="Scan keys">
        <label className="field-inline" htmlFor="key-pattern">
          <span>Pattern</span>
          <input
            id="key-pattern"
            type="text"
            value={pattern}
            placeholder="session:*"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setPattern(e.target.value)}
          />
        </label>
        <label className="field-inline" htmlFor="key-count">
          <span>Count hint</span>
          <select id="key-count" value={count} onChange={(e) => setCount(Number(e.target.value))}>
            {counts.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </label>
        <button type="submit" className="button button-small">
          Scan
        </button>
      </form>
      <p className="muted">
        The pattern is a glob: * matches any run of characters, ? one character, [ab] one of a set. Studio lists keys with SCAN, never KEYS, which would block the server.
      </p>

      {streamed.data.error ? <ErrorNotice error={streamed.data.error} onRetry={streamed.data.retry} /> : null}

      <p className="notice" role="note">
        SCAN is incremental. Each step reads part of the keyspace and returns the keys that match, so a step can return few keys or none even when more exist. The same key can come back twice (Studio shows it once), and keys created while the scan runs may be missed.
      </p>

      <p role="status" data-testid="scan-status">
        {streamed.loaded.toLocaleString("en-US")} {streamed.loaded === 1 ? "key" : "keys"} so far in {streamed.steps} {streamed.steps === 1 ? "step" : "steps"}.{" "}
        {streamed.hasMore ? "The scan is not finished." : "The scan is complete (the cursor came back as 0)."}
        {streamed.duplicates > 0 ? ` ${streamed.duplicates} repeated ${streamed.duplicates === 1 ? "key was" : "keys were"} shown once.` : ""}
      </p>

      <div className="key-grid">
      <Grid
        source=""
        label="Keys"
        columns={columns}
        primaryKey={[]}
        data={data}
        view={view}
        density="normal"
        announce={announce}
        resetKey={`${applied.pattern}|${applied.count}|${applied.nonce}`}
        onOpenRow={(i) => {
          const key = rows[i]?.[0]?.t;
          if (key !== undefined) onOpenKey(key);
        }}
        onSort={(column) => setView(nextSort(view, column))}
        onSetSort={(column, desc) => setView({ ...view, sort: column, desc: column !== null && desc })}
      />
      </div>

      <div className="table-toolbar">
        <button
          type="button"
          className="button"
          onClick={streamed.loadMore}
          disabled={!streamed.hasMore || streamed.loadingMore}
        >
          {streamed.loadingMore ? "Scanning…" : "Load more"}
        </button>
        <span className="muted">Open a key with Enter or a double click on its row.</span>
      </div>
      <LiveRegion message={message} />
    </section>
  );
}
