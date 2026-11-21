import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import type { ApiCell, QueryResponse } from "../api/types";
import { CloseIcon } from "../components/icons";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { copyText } from "../grid/clipboard";
import { nextSort } from "../grid/filters";
import type { ViewState } from "../grid/filters";
import { plainText, toCSV, toJSON } from "../grid/format";
import { Grid } from "../grid/Grid";
import { limitNotice, planText, resultColumns, sortRows, staticGridData, summary } from "./resultData";
import type { RunState } from "./runner";

const noSort: ViewState = { filters: [], sort: null, desc: false };

function download(name: string, mime: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: mime }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function RowDialog({
  columns,
  row,
  index,
  onClose,
}: {
  columns: readonly string[];
  row: readonly ApiCell[] | null;
  index: number;
  onClose: () => void;
}): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (row && !d.open) {
      d.showModal();
    } else if (!row && d.open) {
      d.close();
    }
  }, [row]);

  return (
    <dialog ref={dialog} className="dialog" aria-labelledby="result-row-title" onClose={onClose}>
      <header className="dialog-header">
        <h2 id="result-row-title" className="dialog-title">
          Row {index + 1}
        </h2>
        <button type="button" className="icon-button" aria-label="Close the row" onClick={() => dialog.current?.close()}>
          <CloseIcon />
        </button>
      </header>
      <dl className="row-values">
        {columns.map((name, i) => {
          const cell = row?.[i];
          return (
            <div key={`${i}:${name}`} className="row-value">
              <dt className="mono">{name}</dt>
              <dd className="mono">{cell ? (cell.n ? "NULL" : plainText(cell)) : ""}</dd>
            </div>
          );
        })}
      </dl>
    </dialog>
  );
}

function ResultGrid({ result, runId }: { result: QueryResponse; runId: number }): ReactElement {
  const [view, setView] = useState<ViewState>(noSort);
  const [opened, setOpened] = useState<number | null>(null);
  const { message, announce } = useAnnouncer();

  const columns = useMemo(() => resultColumns(result.columns), [result.columns]);
  const sortIndex = view.sort === null ? null : result.columns.indexOf(view.sort);
  const rows = useMemo(() => sortRows(result.rows, sortIndex, view.desc), [result.rows, sortIndex, view.desc]);
  const data = useMemo(() => staticGridData(rows, 0), [rows]);
  const cut = useMemo(() => result.rows.some((r) => r.some((c) => c.x)), [result.rows]);

  const exportAs = (kind: "csv" | "json"): void => {
    if (kind === "csv") {
      download("result.csv", "text/csv", toCSV(columns, rows));
    } else {
      download("result.json", "application/json", toJSON(columns, rows));
    }
    announce(`Saved ${rows.length} rows as ${kind.toUpperCase()}.`);
  };

  const copyCSV = async (): Promise<void> => {
    announce((await copyText(toCSV(columns, rows))) ? `Copied ${rows.length} rows as CSV.` : "The browser did not allow copying.");
  };

  return (
    <div className="result-grid-block">
      <div className="result-actions">
        <button type="button" className="button button-small" onClick={() => void copyCSV()}>
          Copy as CSV
        </button>
        <button type="button" className="button button-small" onClick={() => exportAs("csv")}>
          Download CSV
        </button>
        <button type="button" className="button button-small" onClick={() => exportAs("json")}>
          Download JSON
        </button>
        {cut ? <span className="muted">Long values are cut for display; copies and downloads hold the cut text.</span> : null}
      </div>
      <div className="result-grid">
        <Grid
          source=""
          label="Query result"
          columns={columns}
          primaryKey={[]}
          data={data}
          view={view}
          density="compact"
          announce={announce}
          resetKey={String(runId)}
          onOpenRow={(i) => setOpened(i)}
          onSort={(column) => setView((v) => nextSort(v, column))}
          onSetSort={(column, desc) => setView({ filters: [], sort: column, desc: column !== null && desc })}
        />
      </div>
      <LiveRegion message={message} />
      <RowDialog columns={result.columns} row={opened === null ? null : (rows[opened] ?? null)} index={opened ?? 0} onClose={() => setOpened(null)} />
    </div>
  );
}

function Running({ state, onCancel }: { state: Extract<RunState, { status: "running" }>; onCancel: () => void }): ReactElement {
  const [now, setNow] = useState(() => performance.now());

  useEffect(() => {
    const t = setInterval(() => setNow(performance.now()), 250);
    return () => clearInterval(t);
  }, []);

  const seconds = Math.max(Math.floor((now - state.startedAt) / 1000), 0);

  return (
    <div className="result-running">
      <p role="status">
        {state.kind === "explain" ? "Explaining" : "Running"}… {seconds} s
      </p>
      <button type="button" className="button" onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}

/** ResultView shows what a run came to: the rows in the grid, a plan, an error or a cancel. */
export function ResultView({ state, onCancel }: { state: RunState; onCancel: () => void }): ReactElement | null {
  switch (state.status) {
    case "idle":
      return null;
    case "running":
      return <Running state={state} onCancel={onCancel} />;
    case "cancelled":
      return (
        <p role="status" className="result-line">
          Cancelled. The statement was stopped on the server when its connection closed.
        </p>
      );
    case "error":
      return (
        <div className="notice notice-error" role="alert">
          <p>
            <strong>{state.error.label}.</strong> {state.error.message}
          </p>
        </div>
      );
    case "done": {
      const { result } = state;
      const notice = limitNotice(result);
      return (
        <div className="result">
          <p role="status" className="result-line">
            <span className="badge">{result.kind || "OK"}</span> {summary(result)}
          </p>
          {notice ? <p className="notice result-notice">{notice}</p> : null}
          {state.kind === "explain" ? (
            <pre className="plan" tabIndex={0} aria-label="Query plan">
              {planText(result)}
            </pre>
          ) : result.columns.length > 0 ? (
            <ResultGrid key={state.id} result={result} runId={state.id} />
          ) : null}
        </div>
      );
    }
  }
}
