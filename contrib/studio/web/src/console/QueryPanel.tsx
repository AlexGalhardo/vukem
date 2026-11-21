import { useRef, useState, useSyncExternalStore } from "react";
import type { ReactElement } from "react";
import type { CompletionSource } from "../editor/complete";
import { Editor } from "../editor/Editor";
import type { EditorHandle, RunScope } from "../editor/Editor";
import { format } from "../editor/format";
import { ResultView } from "./ResultView";
import type { RunKind, RunnerStore } from "./runner";

export interface QueryPanelProps {
  sourceName: string;
  tabId: string;
  text: string;
  onText: (text: string) => void;
  store: RunnerStore;
  catalog: CompletionSource;
}

/** QueryPanel is one query: the editor, the buttons that run it and the result of the last run. */
export function QueryPanel({ sourceName, tabId, text, onText, store, catalog }: QueryPanelProps): ReactElement {
  const editor = useRef<EditorHandle>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const [notice, setNotice] = useState("");
  const state = useSyncExternalStore(store.subscribe, () => store.get(tabId));
  const running = state.status === "running";

  const start = async (kind: RunKind, scope: RunScope): Promise<void> => {
    const statement = editor.current?.statement(scope) ?? "";
    if (statement === "") {
      setNotice("Type a statement to run.");
      return;
    }
    setNotice("");
    await store.start(tabId, kind, statement, false);
    // Focus goes back to the editor unless the person already moved it somewhere else.
    const active = document.activeElement;
    if (active === document.body || (active !== null && toolbar.current?.contains(active))) {
      editor.current?.focus();
    }
  };

  return (
    <div className="query-panel">
      <div className="console-toolbar" ref={toolbar}>
        <button type="button" className="button button-primary" onClick={() => void start("run", "cursor")} disabled={running}>
          Run
        </button>
        <button type="button" className="button" onClick={() => void start("explain", "cursor")} disabled={running}>
          Explain
        </button>
        <button type="button" className="button" onClick={() => store.cancel(tabId)} disabled={!running}>
          Cancel
        </button>
        <button type="button" className="button" onClick={() => editor.current?.insert(format(text))} disabled={text.trim() === ""}>
          Format
        </button>
        <span className="muted console-hint">Ctrl or Cmd+Enter runs the statement at the cursor.</span>
      </div>
      <Editor
        ref={editor}
        value={text}
        onChange={onText}
        label={`SQL for ${sourceName}`}
        completion={catalog}
        onRun={(scope) => void start("run", scope)}
        onSave={() => undefined}
      />
      {notice ? (
        <p role="status" className="muted">
          {notice}
        </p>
      ) : null}
      <ResultView state={state} onCancel={() => store.cancel(tabId)} />
    </div>
  );
}
