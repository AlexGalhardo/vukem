import type { ReactElement } from "react";
import type { Relation } from "./model";

/**
 * RelationsTable lists every foreign key as text: the same information the
 * diagram draws, for whoever cannot or does not want to read a picture.
 */
export function RelationsTable({
  relations,
  selected,
  onOpen,
}: {
  relations: readonly Relation[];
  selected: string | null;
  onOpen: (table: string) => void;
}): ReactElement {
  if (relations.length === 0) {
    return <p className="muted">No table of this schema has a foreign key.</p>;
  }

  return (
    <table className="data-table" aria-label="Relationships">
      <caption className="visually-hidden">Foreign keys: from table and columns, to table and columns</caption>
      <thead>
        <tr>
          <th scope="col">From table</th>
          <th scope="col">Columns</th>
          <th scope="col">To table</th>
          <th scope="col">Columns</th>
        </tr>
      </thead>
      <tbody>
        {relations.map((r, i) => (
          <tr key={`${r.fromTable}:${r.fromColumns.join(",")}:${r.toTable}:${i}`} className={r.fromTable === selected || r.toTable === selected ? "row-active" : undefined}>
            <th scope="row">
              <button type="button" className="link-button" onClick={() => onOpen(r.fromTable)}>
                {r.fromTable}
              </button>
            </th>
            <td className="mono">{r.fromColumns.join(", ")}</td>
            <td>
              <button type="button" className="link-button" onClick={() => onOpen(r.toTable)}>
                {r.toTable}
              </button>
            </td>
            <td className="mono">{r.toColumns.join(", ")}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
