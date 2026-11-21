import { useMemo, useState } from "react";
import type { ReactElement } from "react";
import type { Source } from "../api/types";
import { useCatalog } from "../console/catalog";
import { QueryPanel } from "../console/QueryPanel";
import { RunnerStore } from "../console/runner";

/** ConsolePage is the SQL console of one source. */
export function ConsolePage({ source }: { source: Source; write: boolean }): ReactElement {
  const [text, setText] = useState("");
  const store = useMemo(() => new RunnerStore(source.name), [source.name]);
  const catalog = useCatalog(source.name, text);

  return (
    <section aria-labelledby="console-title">
      <h1 id="console-title">SQL console</h1>
      <p className="muted mono">{source.label}</p>
      <QueryPanel sourceName={source.name} tabId="main" text={text} onText={setText} store={store} catalog={catalog} />
    </section>
  );
}
