import type { ReactElement } from "react";
import type { ApiCell } from "../api/types";
import type { ColumnKind } from "./filters";
import { cellView } from "./format";

/** CellContent draws the value of one cell; the cell element itself belongs to the grid. */
export function CellContent({ cell, kind }: { cell: ApiCell; kind: ColumnKind }): ReactElement {
  const view = cellView(cell, kind);

  if (view.tone === "null") {
    return <span className="token token-null">NULL</span>;
  }
  if (view.tone === "binary") {
    return <span className="token token-binary">{view.text}</span>;
  }
  if (view.tone === "boolean") {
    return <span className={`token token-bool token-${view.text}`}>{view.text}</span>;
  }

  return (
    <span className={`cell-text cell-${view.tone}`}>
      {view.text}
      {cell.x ? (
        <span className="cell-cut" title="The value is cut here. Open the row to read it all.">
          …
        </span>
      ) : null}
    </span>
  );
}

/** PlaceholderCell stands in for a cell whose page has not arrived. */
export function PlaceholderCell(): ReactElement {
  return <span className="cell-placeholder" aria-hidden="true" />;
}
