import { useRef, useState, useSyncExternalStore } from "react";
import type { ReactElement } from "react";
import type { CompletionSource } from "../editor/complete";
import { Editor } from "../editor/Editor";
import type { EditorHandle, RunScope } from "../editor/Editor";
import { format } from "../editor/format";
import { classify } from "./classify";
import { ConfirmWriteDialog } from "./ConfirmWriteDialog";
import type { PendingWrite } from "./ConfirmWriteDialog";
import { ResultView } from "./ResultView";
import type { RunKind, RunnerStore } from "./runner";

export interface QueryPanelProps {
  sourceName: string;
  tabId: string;
  text: string;
  onText: (text: string) => void;
  store: RunnerStore;
  catalog: CompletionSource;
  /** Studio runs with --write: the Write mode switch exists. */
  serverWrite: boolean;
  /** The switch of this tab; off when the tab opens. */
  writeMode: boolean;
  onWriteMode: (on: boolean) => void;
}

/** QueryPanel is one query: the editor, the buttons that run it and the result of the last run. */
export function QueryPanel({
  sourceName,
  tabId,
  text,
  onText,
  store,
  catalog,
  serverWrite,
  writeMode,
  onWriteMode,
}: QueryPanelProps): ReactElement {
  const editor = useRef<EditorHandle>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const [notice, setNotice] = useState("");
  const [pending, setPending] = useState<PendingWrite | null>(null);
  const state = useSyncExternalStore(store.subscribe, () => store.get(tabId));
  const running = state.status === "running";
  const writing = serverWrite && writeMode;

  const send = async (kind: RunKind, statement: string, write: boolean): Promise<void> => {
    await store.start(tabId, kind, statement, write);
    focusEditor();
  };

  // Focus goes back to the editor unless the person already moved it somewhere else.
  const focusEditor = (): void => {
    const active = document.activeElement;
    if (active === document.body || (active !== null && toolbar.current?.contains(active))) {
      editor.current?.focus();
    }
  };

  const start = async (kind: RunKind, scope: RunScope): Promise<void> => {
    const statement = editor.current?.statement(scope) ?? "";
    if (statement === "") {
      setNotice("Type a statement to run.");
      return;
    }
    if (kind === "explain") {
      setNotice("");
      await send("explain", statement, false);
      return;
    }

    const info = classify(statement);
    if (info.write && writing) {
      setNotice("");
      setPending({ statement, info });
      return;
    }

    // The server decides: a write that is not allowed is sent anyway and comes back refused with its own message.
    setNotice(
      info.write
        ? serverWrite
          ? `${info.kind} is not a read. Turn on Write mode to run it; the server refuses it until then.`
          : `${info.kind} is not a read. Studio runs read-only, so the server refuses it.`
        : "",
    );
    await send("run", statement, false);
  };

  const confirm = (): void => {
    const write = pending;
    setPending(null);
    if (write) {
      void store.start(tabId, "run", write.statement, true).then(() => editor.current?.focus());
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
        <span className={writing ? "badge badge-write" : "badge badge-readonly"} data-testid="console-mode">
          {writing ? "Write mode" : "Read-only"}
        </span>
        {serverWrite ? (
          <button
            type="button"
            role="switch"
            aria-checked={writeMode}
            className="button"
            onClick={() => onWriteMode(!writeMode)}
          >
            Write mode
          </button>
        ) : null}
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
      <ConfirmWriteDialog pending={pending} source={sourceName} onConfirm={confirm} onCancel={() => setPending(null)}
        onAfterClose={() => editor.current?.focus()} />
    </div>
  );
}
