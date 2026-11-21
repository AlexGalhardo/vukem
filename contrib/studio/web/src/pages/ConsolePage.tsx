import type { ReactElement } from "react";
import type { Source } from "../api/types";
import { Workspace } from "../tabs/Workspace";

/** ConsolePage is the workspace of one SQL source: query and table tabs. */
export function ConsolePage({
  source,
  write,
  refreshSeconds,
}: {
  source: Source;
  write: boolean;
  refreshSeconds: number;
}): ReactElement {
  return (
    <section aria-labelledby="console-title">
      <h1 id="console-title">SQL console</h1>
      <p className="muted mono">{source.label}</p>
      <Workspace key={source.name} source={source} write={write} refreshSeconds={refreshSeconds} />
    </section>
  );
}
