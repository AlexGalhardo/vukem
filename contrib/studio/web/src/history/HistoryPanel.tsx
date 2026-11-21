import { useState, useSyncExternalStore } from "react";
import type { ReactElement } from "react";
import { ConfirmDialog } from "../console/ConfirmDialog";
import type { ConfirmRequest } from "../console/ConfirmDialog";
import { copyText } from "../grid/clipboard";
import { matches, maxHistory, maxSaved } from "./store";
import type { HistoryEntry, HistoryStore, SavedQuery } from "./store";

const outcomeLabels: Record<string, string> = {
  ok: "ok",
  cancelled: "cancelled",
  input: "input error",
  timeout: "timeout",
  database: "database error",
  refused: "refused",
  network: "connection error",
  other: "error",
};

function preview(sql: string): string {
  const flat = sql.replace(/\s+/g, " ").trim();
  return flat.length > 60 ? flat.slice(0, 57) + "..." : flat;
}

function meta(e: HistoryEntry): string {
  const parts = [new Date(e.at).toLocaleTimeString(), outcomeLabels[e.outcome] ?? e.outcome];
  if (e.kind !== "") {
    parts.push(e.kind);
  }
  if (e.rows !== null) {
    parts.push(`${e.rows} ${e.rows === 1 ? "row" : "rows"}`);
  }
  if (e.outcome === "ok") {
    parts.push(`${e.durationMs} ms`);
  }
  return parts.join(" · ");
}

export interface HistoryPanelProps {
  store: HistoryStore;
  source: string;
  /** Loads a statement into a new tab. It never runs it. */
  onOpen: (sql: string, title?: string) => void;
  onRename: (query: SavedQuery) => void;
  /** Tells a screen reader what happened (copied, deleted, cleared). */
  say: (text: string) => void;
}

/** HistoryPanel lists the saved queries and the run history of one source, both kept in this browser only. */
export function HistoryPanel({ store, source, onOpen, onRename, say }: HistoryPanelProps): ReactElement {
  const snap = useSyncExternalStore(store.subscribe, store.getSnapshot);
  const [query, setQuery] = useState("");
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null);

  const saved = snap.saved.filter((q) => q.source === source);
  const entries = snap.history
    .filter((e) => e.source === source && matches(e, query))
    .slice()
    .reverse();

  const copy = async (sql: string): Promise<void> => {
    say((await copyText(sql)) ? "Statement copied." : "The browser did not allow copying.");
  };

  return (
    <aside className="history" aria-label="History and saved queries">
      <p className="muted history-note">
        History is stored in this browser until you clear it, and it holds the statements you ran, with any values in them. Results are never stored.
        Saved queries stay across sessions.
      </p>
      <div className="history-columns">
        <section aria-labelledby="saved-title" className="history-column">
          <h2 id="saved-title" className="history-title">
            Saved queries <span className="muted">({saved.length} of {maxSaved})</span>
          </h2>
          {saved.length === 0 ? (
            <p className="muted">Nothing saved yet. Press Ctrl or Cmd+S in the editor to save a query.</p>
          ) : (
            <ul className="history-list" aria-label="Saved queries">
              {saved.map((q) => (
                <li key={q.id} className="history-item">
                  <p className="history-name">{q.name}</p>
                  <pre className="history-sql">{q.sql}</pre>
                  <div className="history-actions">
                    <button type="button" className="button button-small" aria-label={`Open saved query ${q.name} in a new tab`} onClick={() => onOpen(q.sql, q.name)}>
                      Open
                    </button>
                    <button type="button" className="button button-small" aria-label={`Rename saved query ${q.name}`} onClick={() => onRename(q)}>
                      Rename
                    </button>
                    <button
                      type="button"
                      className="button button-small"
                      aria-label={`Delete saved query ${q.name}`}
                      onClick={() => {
                        store.deleteSaved(q.id);
                        say(`Deleted ${q.name}.`);
                      }}
                    >
                      Delete
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section aria-labelledby="history-title" className="history-column">
          <h2 id="history-title" className="history-title">
            History <span className="muted">(last {maxHistory})</span>
          </h2>
          <div className="history-controls">
            <label className="field-inline">
              <span className="visually-hidden">Search the history</span>
              <input type="search" placeholder="Search the history" value={query} onChange={(e) => setQuery(e.target.value)} />
            </label>
            <button
              type="button"
              className="button button-small"
              disabled={snap.history.length === 0}
              onClick={() =>
                setConfirm({
                  title: "Clear the history?",
                  message: `This deletes the ${snap.history.length} statements kept in this browser. Saved queries stay.`,
                  confirmLabel: "Clear history",
                })
              }
            >
              Clear history
            </button>
            <label className="field-inline">
              <input
                type="checkbox"
                checked={!snap.enabled}
                onChange={(e) => {
                  store.setEnabled(!e.target.checked);
                  say(e.target.checked ? "History is off and was cleared." : "History is on.");
                }}
              />
              Do not store history
            </label>
          </div>
          {!snap.enabled ? <p className="muted">History is off: nothing you run is kept.</p> : null}
          {snap.enabled && entries.length === 0 ? (
            <p className="muted">{query.trim() === "" ? "No statements yet." : "No statement matches."}</p>
          ) : (
            <ul className="history-list" aria-label="History">
              {entries.map((e) => (
                <li key={e.id} className="history-item">
                  <pre className="history-sql">{e.sql}</pre>
                  <p className="muted history-meta">{meta(e)}</p>
                  <div className="history-actions">
                    <button
                      type="button"
                      className="button button-small"
                      aria-label={`Open in a new tab: ${preview(e.sql)}`}
                      onClick={() => onOpen(e.sql)}
                    >
                      Open in new tab
                    </button>
                    <button type="button" className="button button-small" aria-label={`Copy: ${preview(e.sql)}`} onClick={() => void copy(e.sql)}>
                      Copy
                    </button>
                    <button
                      type="button"
                      className="button button-small"
                      aria-label={`Delete from history: ${preview(e.sql)}`}
                      onClick={() => {
                        store.remove(e.id);
                        say("Deleted from the history.");
                      }}
                    >
                      Delete
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
      <ConfirmDialog
        request={confirm}
        onConfirm={() => {
          setConfirm(null);
          store.clear();
          say("History cleared.");
        }}
        onCancel={() => setConfirm(null)}
        onAfterClose={() => undefined}
      />
    </aside>
  );
}
