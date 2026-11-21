import { useCallback, useEffect, useId, useMemo, useReducer, useRef, useState } from "react";
import type { ReactElement } from "react";
import type { Source } from "../api/types";
import { useCatalog } from "../console/catalog";
import { ConfirmDialog } from "../console/ConfirmDialog";
import type { ConfirmRequest } from "../console/ConfirmDialog";
import { QueryPanel } from "../console/QueryPanel";
import { RunnerStore } from "../console/runner";
import type { RunState } from "../console/runner";
import { localStore } from "../history/browser";
import { HistoryPanel } from "../history/HistoryPanel";
import { NameDialog } from "../history/NameDialog";
import type { NameRequest } from "../history/NameDialog";
import { entryFrom, openHistory } from "../history/store";
import type { SavedQuery } from "../history/store";
import { replaceSearch } from "../lib/history";
import { useShortcut } from "../shortcuts/ShortcutsProvider";
import { TablePage } from "../pages/TablePage";
import { SchemaView } from "../schema/SchemaView";
import { loadTabs, saveTabs } from "./persist";
import { panelDomId, tabDomId, TabStrip } from "./TabStrip";
import { canOpen, emptyTabs, isUnsaved, maxTabs, tabsReducer } from "./tabsState";
import type { Tab, TabsAction, TabsState } from "./tabsState";

export function newTabId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `t${Date.now().toString(36)}${Math.random().toString(36).slice(2)}`;
}

function initialState(source: string): TabsState {
  const saved = loadTabs(window.sessionStorage, source);
  return saved ?? tabsReducer(emptyTabs, { type: "openQuery", id: newTabId() });
}

const persistDelay = 300;

export interface WorkspaceProps {
  source: Source;
  write: boolean;
  refreshSeconds: number;
  sessionKey: string;
}

/** Workspace is the tabs of one source: queries and tables side by side, kept for the browser tab's session. */
export function Workspace({ source, write, refreshSeconds, sessionKey }: WorkspaceProps): ReactElement {
  const idPrefix = useId();
  const [state, rawDispatch] = useReducer(tabsReducer, source.name, initialState);
  const [writeModes, setWriteModes] = useState<Record<string, boolean>>({});
  const [confirm, setConfirm] = useState<{ request: ConfirmRequest; run: () => void } | null>(null);
  const [notice, setNotice] = useState("");
  const store = useMemo(() => new RunnerStore(source.name), [source.name]);
  const strip = useRef<HTMLDivElement>(null);
  const history = useMemo(() => openHistory(localStore(), sessionKey), [sessionKey]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [naming, setNaming] = useState<{ request: NameRequest; tabId: string | null; rename: SavedQuery | null; sql: string } | null>(null);
  const [nameError, setNameError] = useState("");
  const [said, setSaid] = useState("");

  const activeTab = state.tabs.find((t) => t.id === state.active) ?? null;
  const catalog = useCatalog(source.name, activeTab?.kind === "query" ? activeTab.text : "");

  // A table tab keeps its view in the URL's query string: another tab must not inherit it.
  const dispatch = useCallback((action: TabsAction): void => {
    if (action.type !== "setText" && action.type !== "markSaved" && window.location.search !== "") {
      replaceSearch("");
    }
    rawDispatch(action);
  }, []);

  const latest = useRef(state);
  latest.current = state;

  useEffect(() => {
    const timer = setTimeout(() => saveTabs(window.sessionStorage, source.name, state), persistDelay);
    return () => clearTimeout(timer);
  }, [state, source.name]);

  useEffect(() => {
    const flush = (): void => saveTabs(window.sessionStorage, source.name, latest.current);
    window.addEventListener("pagehide", flush);
    return () => {
      window.removeEventListener("pagehide", flush);
      flush();
    };
  }, [source.name]);

  const openQuery = useCallback((text?: string): void => {
    if (!canOpen(latest.current)) {
      setNotice(`${maxTabs} tabs are open. Close one to open another.`);
      return;
    }
    setNotice("");
    dispatch({ type: "openQuery", id: newTabId(), text });
  }, [dispatch]);

  const closeNow = useCallback(
    (id: string): void => {
      store.forget(id);
      setWriteModes((prev) => {
        const { [id]: _gone, ...rest } = prev;
        return rest;
      });
      dispatch({ type: "close", id });
    },
    [dispatch, store],
  );

  const requestClose = (tab: Tab): void => {
    if (isUnsaved(tab) && tab.text.trim() !== "") {
      setConfirm({
        request: {
          title: `Close ${tab.title}?`,
          message: "Its query text has not been saved to the library. Closing the tab discards it.",
          confirmLabel: "Close tab",
        },
        run: () => closeNow(tab.id),
      });
      return;
    }
    closeNow(tab.id);
  };

  const stepTab = (delta: 1 | -1): void => {
    dispatch({ type: "step", delta });
    // The strip owns the focus ring of the selected tab: move it there so the next key works from it.
    requestAnimationFrame(() => strip.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus());
  };

  useShortcut({ key: "q n", description: "New query tab", run: () => openQuery() });
  useShortcut({ key: "w c", description: "Close the active tab", run: () => activeTab && requestClose(activeTab), enabled: () => activeTab !== null });
  useShortcut({ key: "]", description: "Next tab", run: () => stepTab(1), enabled: () => state.tabs.length > 1 });
  useShortcut({ key: "[", description: "Previous tab", run: () => stepTab(-1), enabled: () => state.tabs.length > 1 });

  const openTable = (table: string): void => {
    if (table === "") {
      return;
    }
    if (!canOpen(state) && !state.tabs.some((t) => t.kind === "table" && t.table === table)) {
      setNotice(`${maxTabs} tabs are open. Close one to open another.`);
      return;
    }
    setNotice("");
    dispatch({ type: "openTable", id: newTabId(), table });
  };

  const openSchema = (): void => {
    if (!canOpen(state) && !state.tabs.some((t) => t.kind === "schema")) {
      setNotice(`${maxTabs} tabs are open. Close one to open another.`);
      return;
    }
    setNotice("");
    dispatch({ type: "openSchema", id: newTabId() });
  };

  const say = useCallback((text: string): void => {
    // The same sentence twice would not be read twice.
    setSaid((prev) => (prev === text ? `${text} ` : text));
  }, []);

  const record = (state: RunState): void => {
    const entry = entryFrom(newTabId(), source.name, state, Date.now());
    if (entry) {
      history.add(entry);
    }
  };

  const requestSave = (tab: Tab): void => {
    if (tab.text.trim() === "") {
      say("Type a query before saving it.");
      return;
    }
    setNameError("");
    setNaming({
      request: { title: "Save query", label: "Name", initial: tab.title, confirmLabel: "Save" },
      tabId: tab.id,
      rename: null,
      sql: tab.text,
    });
  };

  const requestRename = (query: SavedQuery): void => {
    setNameError("");
    setNaming({
      request: { title: "Rename saved query", label: "New name", initial: query.name, confirmLabel: "Rename" },
      tabId: null,
      rename: query,
      sql: query.sql,
    });
  };

  const confirmName = (name: string): void => {
    if (!naming) {
      return;
    }
    if (naming.rename) {
      if (!history.rename(naming.rename.id, name)) {
        setNameError("That name is already used by another saved query.");
        return;
      }
      say(`Renamed to ${name.trim()}.`);
    } else {
      const result = history.save(name, naming.sql, source.name, newTabId(), Date.now());
      if (result === "full") {
        setNameError("The library holds 100 queries. Delete one first.");
        return;
      }
      if (result === "invalid") {
        setNameError("Give the query a name.");
        return;
      }
      if (naming.tabId !== null) {
        dispatch({ type: "markSaved", id: naming.tabId });
        dispatch({ type: "rename", id: naming.tabId, title: name.trim() });
      }
      say(`${result === "replaced" ? "Replaced" : "Saved"} ${name.trim()}.`);
    }
    setNaming(null);
  };

  const openFromHistory = (sql: string, title?: string): void => {
    if (!canOpen(latest.current)) {
      setNotice(`${maxTabs} tabs are open. Close one to open another.`);
      return;
    }
    setNotice("");
    const id = newTabId();
    dispatch({ type: "openQuery", id, text: sql });
    if (title) {
      dispatch({ type: "rename", id, title });
    }
  };

  const closeOthers = (tab: Tab): void => {
    const others = state.tabs.filter((t) => t.id !== tab.id && !t.pinned);
    const losing = others.filter((t) => isUnsaved(t) && t.text.trim() !== "");
    const go = (): void => {
      others.forEach((t) => store.forget(t.id));
      dispatch({ type: "closeOthers", id: tab.id });
    };
    if (losing.length > 0) {
      setConfirm({
        request: {
          title: "Close the other tabs?",
          message: `${losing.length} of them ${losing.length === 1 ? "has" : "have"} query text that was not saved to the library.`,
          confirmLabel: "Close others",
        },
        run: go,
      });
      return;
    }
    go();
  };

  return (
    <div className="workspace">
      <div className="workspace-bar">
        <div className="workspace-strip" ref={strip}>
          <TabStrip
            idPrefix={idPrefix}
            tabs={state.tabs}
            active={state.active}
            onActivate={(id) => dispatch({ type: "activate", id })}
            onClose={(id) => {
              const tab = state.tabs.find((t) => t.id === id);
              if (tab) {
                requestClose(tab);
              }
            }}
            onShift={(id, delta) => dispatch({ type: "shift", id, delta })}
            onMove={(id, to) => dispatch({ type: "move", id, to })}
          />
        </div>
        <button type="button" className="button" onClick={() => openQuery()}>
          New query
        </button>
        <button type="button" className="button" onClick={() => openSchema()}>
          Schema
        </button>
        <button
          type="button"
          className="button"
          aria-expanded={historyOpen}
          aria-controls={`${idPrefix}-history`}
          onClick={() => setHistoryOpen((o) => !o)}
        >
          History and saved queries
        </button>
        <label className="field-inline">
          <span className="visually-hidden">Open a table in a tab</span>
          <select
            value=""
            aria-label="Open a table in a tab"
            onChange={(e) => openTable(e.target.value)}
          >
            <option value="">Open table…</option>
            {catalog.tables.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="button" onClick={() => stepTab(-1)} disabled={state.tabs.length < 2}>
          Previous tab
        </button>
        <button type="button" className="button" onClick={() => stepTab(1)} disabled={state.tabs.length < 2}>
          Next tab
        </button>
        {activeTab ? (
          <>
            <button
              type="button"
              className="button"
              onClick={() => dispatch({ type: "pin", id: activeTab.id, pinned: !activeTab.pinned })}
            >
              {activeTab.pinned ? "Unpin tab" : "Pin tab"}
            </button>
            {activeTab.kind === "query" ? (
              <button
                type="button"
                className="button"
                onClick={() => {
                  if (!canOpen(state)) {
                    setNotice(`${maxTabs} tabs are open. Close one to open another.`);
                  } else {
                    setNotice("");
                    dispatch({ type: "duplicate", id: activeTab.id, newId: newTabId() });
                  }
                }}
              >
                Duplicate tab
              </button>
            ) : null}
            <button type="button" className="button" onClick={() => closeOthers(activeTab)} disabled={state.tabs.length < 2}>
              Close other tabs
            </button>
            <button type="button" className="button" onClick={() => requestClose(activeTab)}>
              Close tab
            </button>
          </>
        ) : null}
      </div>
      {notice ? (
        <p role="status" className="muted">
          {notice}
        </p>
      ) : (
        <p role="status" className="visually-hidden">
          {state.tabs.length} {state.tabs.length === 1 ? "tab" : "tabs"} open
        </p>
      )}

      {historyOpen ? (
        <div id={`${idPrefix}-history`}>
          <HistoryPanel store={history} source={source.name} onOpen={openFromHistory} onRename={requestRename} say={say} />
        </div>
      ) : null}
      <p className="visually-hidden" role="status">
        {said}
      </p>

      {activeTab === null ? (
        <div className="empty">
          <p className="empty-title">No tab is open</p>
          <button type="button" className="button button-primary" onClick={() => openQuery()}>
            New query
          </button>
        </div>
      ) : (
        <div
          key={activeTab.id}
          role="tabpanel"
          id={panelDomId(idPrefix, activeTab.id)}
          aria-labelledby={tabDomId(idPrefix, activeTab.id)}
          className="workspace-panel"
        >
          {activeTab.kind === "table" && activeTab.table !== undefined ? (
            <TablePage source={source.name} table={activeTab.table} write={write} refreshSeconds={refreshSeconds} />
          ) : activeTab.kind === "schema" ? (
            <SchemaView source={source.name} onOpenTable={openTable} />
          ) : (
            <QueryPanel
              sourceName={source.name}
              tabId={activeTab.id}
              text={activeTab.text}
              onText={(text) => dispatch({ type: "setText", id: activeTab.id, text })}
              store={store}
              catalog={catalog}
              serverWrite={write}
              writeMode={writeModes[activeTab.id] === true}
              onWriteMode={(on) => setWriteModes((prev) => ({ ...prev, [activeTab.id]: on }))}
              onRan={record}
              onSave={() => requestSave(activeTab)}
            />
          )}
        </div>
      )}

      <NameDialog
        request={naming?.request ?? null}
        error={nameError}
        onConfirm={confirmName}
        onCancel={() => setNaming(null)}
        onAfterClose={() => {
          document.querySelector<HTMLTextAreaElement>(".sqled-input")?.focus();
        }}
      />
      <ConfirmDialog
        request={confirm?.request ?? null}
        onConfirm={() => {
          const run = confirm?.run;
          setConfirm(null);
          run?.();
        }}
        onCancel={() => setConfirm(null)}
        onAfterClose={() => {
          const selected = strip.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
          selected?.focus();
        }}
      />
    </div>
  );
}
