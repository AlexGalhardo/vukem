import { useRef, useState } from "react";
import type { ReactElement } from "react";
import type { Source } from "../api/types";
import { useCatalog } from "../console/catalog";
import { Editor } from "../editor/Editor";
import type { EditorHandle, RunScope } from "../editor/Editor";

/** ConsolePage is the SQL console of one source. */
export function ConsolePage({ source }: { source: Source; write: boolean }): ReactElement {
  const [text, setText] = useState("");
  const [requested, setRequested] = useState<string | null>(null);
  const editor = useRef<EditorHandle>(null);
  const catalog = useCatalog(source.name, text);

  const run = (scope: RunScope): void => setRequested(editor.current?.statement(scope) ?? "");

  return (
    <section aria-labelledby="console-title">
      <h1 id="console-title">SQL console</h1>
      <p className="muted mono">{source.label}</p>
      <div className="console-toolbar">
        <button type="button" className="button button-primary" onClick={() => run("cursor")}>
          Run
        </button>
      </div>
      <Editor
        ref={editor}
        value={text}
        onChange={setText}
        label={`SQL for ${source.name}`}
        completion={catalog}
        onRun={run}
        onSave={() => undefined}
      />
      {requested !== null ? <pre data-testid="run-request">{requested}</pre> : null}
    </section>
  );
}
