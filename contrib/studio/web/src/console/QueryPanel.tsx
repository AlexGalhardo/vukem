import { useCallback, useRef, useState, useSyncExternalStore } from "react";
import type { ReactElement } from "react";
import type { SourceKind } from "../api/types";
import type { CompletionSource } from "../editor/complete";
import { Editor } from "../editor/Editor";
import type { EditorHandle, EditorLanguage, RunScope } from "../editor/Editor";
import { format } from "../editor/format";
import { checkConsole, parseJSON } from "../mongo/json";
import { classifyCommand } from "../redis/commands";
import { classify } from "./classify";
import type { StatementInfo } from "./classify";
import { ConfirmWriteDialog } from "./ConfirmWriteDialog";
import type { PendingWrite } from "./ConfirmWriteDialog";
import { summary } from "./resultData";
import { ResultView } from "./ResultView";
import type { RunKind, RunnerStore, RunState } from "./runner";

export interface QueryPanelProps {
  sourceName: string;
  /** What kind of console this is. Without it, SQL. */
  kind?: SourceKind;
  /** The editor language of a MongoDB or Redis console. */
  language?: EditorLanguage;
  tabId: string;
  text: string;
  onText: (text: string) => void;
  store: RunnerStore;
  catalog?: CompletionSource;
  /** Studio runs with --write: the Write mode switch exists. */
  serverWrite: boolean;
  /** The switch of this tab; off when the tab opens. */
  writeMode: boolean;
  onWriteMode: (on: boolean) => void;
  /** A run ended: for the history. */
  onRan: (state: RunState) => void;
  /** Ctrl or Cmd+S, or the Save button: save this query to the library. */
  onSave: () => void;
}

/** QueryPanel is one query: the editor, the buttons that run it and the result of the last run. */
const noCatalog: CompletionSource = { tables: [], columns: () => undefined };

const runNotice: Record<SourceKind, string> = {
  sql: "Ctrl or Cmd+Enter runs the statement at the cursor.",
  mongo: "Ctrl or Cmd+Enter runs the find or aggregate in the editor.",
  redis: "Ctrl or Cmd+Enter runs the command on the caret's line.",
};

const labelOf: Record<SourceKind, string> = { sql: "SQL", mongo: "MongoDB find or aggregate", redis: "Redis command" };

/** What a console that is not SQL says before it sends: nothing to send, a refusal, or a write that needs a confirmation. */
type Prepared = { send: string; confirm: StatementInfo | null; notice: string } | { refuse: string };

function prepare(kind: SourceKind, statement: string, serverWrite: boolean, writing: boolean): Prepared {
  if (kind === "mongo") {
    const check = checkConsole(statement, writing);
    if (!check.ok) {
      return { refuse: serverWrite && !writing && /needs Studio to run with --write/.test(check.message) ? check.message.replace("needs Studio to run with --write", "needs Write mode") : check.message };
    }
    const r = check.request;
    if (r.kind === "aggregate" && /"\$(out|merge)"/.test(r.pipeline)) {
      const stage = r.pipeline.includes('"$out"') ? "$out" : "$merge";
      return {
        send: statement,
        confirm: { kind: "AGGREGATE", write: true, destructive: true, warning: `${stage} writes its result into a collection and can replace what it holds.` },
        notice: "",
      };
    }
    return { send: statement, confirm: null, notice: "" };
  }
  const info = classifyCommand(statement);
  if (!info.known) {
    return { refuse: `${info.name || "That"} is not a command Studio allows. The console takes a fixed list of read commands, and write commands with --write.` };
  }
  if (info.write && !serverWrite) {
    return { refuse: `${info.name} writes data. Studio runs read-only, so the server refuses it; start it with --write.` };
  }
  if (info.write && !writing) {
    return { refuse: `${info.name} writes data. Turn on Write mode to run it.` };
  }
  if (info.write) {
    return {
      send: statement,
      confirm: { kind: info.name, write: true, destructive: info.destructive, warning: `${info.name} removes data and cannot be undone.` },
      notice: "",
    };
  }
  return { send: statement, confirm: null, notice: "" };
}

/** prettyJSON formats the text of a MongoDB console as indented JSON, or says why it cannot. */
function prettyJSON(text: string): string | null {
  const r = parseJSON(text);
  return r.ok ? JSON.stringify(r.value, null, 2) : null;
}

export function QueryPanel({
  sourceName,
  kind = "sql",
  language,
  tabId,
  text,
  onText,
  store,
  catalog = noCatalog,
  serverWrite,
  writeMode,
  onWriteMode,
  onRan,
  onSave,
}: QueryPanelProps): ReactElement {
  const editor = useRef<EditorHandle>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const [notice, setNotice] = useState("");
  const [pending, setPending] = useState<PendingWrite | null>(null);
  const [reply, setReply] = useState<"text" | "table">("text");
  const state = useSyncExternalStore(store.subscribe, () => store.get(tabId));
  const running = state.status === "running";
  const writing = serverWrite && writeMode;
  const cancel = useCallback(() => store.cancel(tabId), [store, tabId]);

  const send = async (runKind: RunKind, statement: string, write: boolean): Promise<void> => {
    await store.start(tabId, runKind, statement, write);
    onRan(store.get(tabId));
    focusEditor();
  };

  // Focus goes back to the editor unless the person already moved it somewhere else.
  const focusEditor = (): void => {
    const active = document.activeElement;
    if (active === document.body || (active !== null && toolbar.current?.contains(active))) {
      editor.current?.focus();
    }
  };

  const start = async (runKind: RunKind, scope: RunScope): Promise<void> => {
    const statement = editor.current?.statement(scope) ?? "";
    if (statement === "") {
      setNotice(kind === "sql" ? "Type a statement to run." : kind === "mongo" ? "Write a find or an aggregate to run." : "Type a command to run.");
      return;
    }
    if (kind !== "sql") {
      const prepared = prepare(kind, statement, serverWrite, writing);
      if ("refuse" in prepared) {
        setNotice(prepared.refuse);
        return;
      }
      setNotice(prepared.notice);
      if (prepared.confirm) {
        setPending({ statement, info: prepared.confirm });
      } else {
        await send("run", statement, writing);
      }
      return;
    }
    if (runKind === "explain") {
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
      void store.start(tabId, "run", write.statement, true).then(() => {
        onRan(store.get(tabId));
        editor.current?.focus();
      });
    }
  };

  return (
    <div className="query-panel">
      <div className="console-toolbar" ref={toolbar}>
        <button type="button" className="button button-primary" onClick={() => void start("run", "cursor")} disabled={running}>
          Run
        </button>
        {kind === "sql" ? (
          <button type="button" className="button" onClick={() => void start("explain", "cursor")} disabled={running}>
            Explain
          </button>
        ) : null}
        <button type="button" className="button" onClick={() => store.cancel(tabId)} disabled={!running}>
          Cancel
        </button>
        {kind !== "redis" ? (
          <button
            type="button"
            className="button"
            onClick={() => {
              const pretty = kind === "mongo" ? prettyJSON(text) : format(text);
              if (pretty === null) {
                setNotice("The text is not valid JSON, so it cannot be formatted.");
              } else {
                editor.current?.insert(pretty);
              }
            }}
            disabled={text.trim() === ""}
          >
            Format
          </button>
        ) : null}
        <button type="button" className="button" onClick={onSave} disabled={text.trim() === ""}>
          Save query
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
        <span className="muted console-hint">{runNotice[kind]}</span>
      </div>
      <Editor
        ref={editor}
        value={text}
        onChange={onText}
        label={`${labelOf[kind]} for ${sourceName}`}
        completion={catalog}
        language={language}
        onRun={(scope) => void start("run", scope)}
        onSave={onSave}
      />
      {notice ? (
        <p role="status" className="muted">
          {notice}
        </p>
      ) : null}
      {kind === "redis" && state.status === "done" ? (
        <div className="reply-tools" role="group" aria-label="Reply view">
          <button type="button" className="button button-small" aria-pressed={reply === "text"} onClick={() => setReply("text")}>
            Text
          </button>
          <button type="button" className="button button-small" aria-pressed={reply === "table"} onClick={() => setReply("table")}>
            Table
          </button>
        </div>
      ) : null}
      {kind === "redis" && state.status === "done" && reply === "text" ? (
        <div className="result">
          <p role="status" className="result-line">
            <span className="badge">{state.result.kind}</span> {summary(state.result)}
          </p>
          <pre className="plan" tabIndex={0} aria-label="Reply">
            {state.result.text ?? ""}
          </pre>
        </div>
      ) : (
        <ResultView state={state} onCancel={cancel} />
      )}
      <ConfirmWriteDialog
        pending={pending}
        source={sourceName}
        note={kind === "sql" ? undefined : kind === "mongo" ? "The pipeline runs as written." : "The command runs as written and cannot be undone."}
        onConfirm={confirm}
        onCancel={() => setPending(null)}
        onAfterClose={() => editor.current?.focus()}
      />
    </div>
  );
}
