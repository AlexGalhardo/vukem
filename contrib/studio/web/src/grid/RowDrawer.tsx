import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { ApiCell, Column, ReferencesResponse, RowResponse, TableDetail, TableSummary } from "../api/types";
import { useApi } from "../api/useApi";
import { CloseIcon, LinkIcon } from "../components/icons";
import { ErrorNotice, Loading } from "../components/Notices";
import { Link } from "../lib/history";
import { columnKind } from "./filters";
import { jsonPretty } from "./format";
import { foreignKeyLink, referenceLink } from "./links";
import type { RowLink } from "./links";
import { confirmText, packKey } from "./rowKey";

/** What the drawer shows: a row named by its key (fetched in full), or cells already in hand (a table without a key). */
export type DrawerTarget = { kind: "key"; key: string[] } | { kind: "cells"; cells: ApiCell[]; number: number };

interface RowDrawerProps {
  source: string;
  table: string;
  detail: TableDetail;
  tables: readonly TableSummary[];
  target: DrawerTarget | null;
  onClose: () => void;
  /** Write mode only: asks to delete the row with this key. */
  onDelete?: (key: string[]) => void;
}

function FieldValue({ cell, column }: { cell: ApiCell; column: Column }): ReactElement {
  const kind = columnKind(column.type);
  const pretty = kind === "json" && !cell.n && !cell.x ? jsonPretty(cell.t) : null;
  const [raw, setRaw] = useState(false);

  if (cell.n) {
    return <span className="token token-null">NULL</span>;
  }
  if (cell.b) {
    return <span className="token token-binary">{cell.t}</span>;
  }

  return (
    <>
      {pretty !== null ? (
        <button
          type="button"
          className="button button-small drawer-toggle"
          aria-pressed={!raw}
          onClick={() => setRaw(!raw)}
        >
          {raw ? "Show formatted" : "Show raw"}
        </button>
      ) : null}
      <pre className="drawer-value">{pretty !== null && !raw ? pretty : cell.t}</pre>
      {cell.x ? <p className="muted">This value is longer than 64 KiB and is cut here.</p> : null}
    </>
  );
}

function Links({ source, title, links }: { source: string; title: string; links: RowLink[] }): ReactElement | null {
  if (links.length === 0) {
    return null;
  }
  return (
    <section className="drawer-section" aria-label={title}>
      <h3 className="drawer-heading">{title}</h3>
      <ul className="drawer-links">
        {links.map((l) => (
          <li key={l.label + l.search}>
            <Link route={{ name: "table", source, table: l.table }} search={l.search}>
              <LinkIcon /> {l.label}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * RowDrawer shows one row in full: every value up to 64 KiB, JSON formatted,
 * the foreign keys as links, and the tables that point at the row. It is a
 * native modal dialog, so the page behind it is inert and Escape closes it.
 */
export function RowDrawer({ source, table, detail, tables, target, onClose, onDelete }: RowDrawerProps): ReactElement {
  const dialog = useRef<HTMLDialogElement>(null);
  const keyed = target?.kind === "key" ? target.key : null;

  const row = useApi<RowResponse>(keyed ? apiPath("sources", source, "tables", table, "rows", packKey(keyed)) : null);
  const refs = useApi<ReferencesResponse>(target ? apiPath("sources", source, "tables", table, "references") : null);

  useEffect(() => {
    const d = dialog.current;
    if (!d) {
      return;
    }
    if (target && !d.open) {
      d.showModal();
    } else if (!target && d.open) {
      d.close();
    }
  }, [target]);

  const cells = target?.kind === "cells" ? target.cells : row.data?.cells;
  const columns = target?.kind === "cells" ? detail.columns : (row.data?.columns ?? detail.columns);
  const title = keyed ? `${table} ${confirmText(keyed)}` : target ? `${table} row ${target.kind === "cells" ? target.number : ""}` : table;

  const fkLinks: Record<string, RowLink> = {};
  if (cells) {
    for (const fk of detail.foreignKeys) {
      const link = foreignKeyLink(fk, columns, cells, tables);
      if (link) {
        for (const c of fk.columns) {
          fkLinks[c] = link;
        }
      }
    }
  }

  const refLinks: RowLink[] = [];
  if (cells && refs.data) {
    for (const ref of refs.data.references) {
      const link = referenceLink(ref, columns, cells);
      if (link) {
        refLinks.push(link);
      }
    }
  }

  return (
    <dialog
      ref={dialog}
      className="drawer"
      aria-labelledby="drawer-title"
      onClose={onClose}
      onClick={(e) => {
        // A click on the backdrop lands on the dialog element itself.
        if (e.target === dialog.current) {
          dialog.current?.close();
        }
      }}
    >
      <header className="drawer-header">
        <h2 id="drawer-title" className="drawer-title">
          {title}
        </h2>
        <button type="button" className="icon-button" aria-label="Close the row" onClick={() => dialog.current?.close()}>
          <CloseIcon />
        </button>
      </header>

      {target ? (
        <div className="drawer-body">
          {keyed && row.loading ? <Loading what="the row" /> : null}
          {keyed && row.error ? <ErrorNotice error={row.error} onRetry={row.reload} /> : null}
          {target.kind === "cells" ? (
            <p className="muted">This table has no primary key, so values longer than 200 characters stay cut.</p>
          ) : null}

          {cells ? (
            <dl className="drawer-fields">
              {columns.map((c, i) => {
                const cell = cells[i];
                const link = fkLinks[c.name];
                return (
                  <div key={c.name} className="drawer-field">
                    <dt>
                      <span className="mono">{c.name}</span>
                      <span className="hcell-type"> {c.type}</span>
                      {detail.primaryKey.includes(c.name) ? <span className="hcell-pk">PK</span> : null}
                    </dt>
                    <dd>
                      {cell ? <FieldValue cell={cell} column={c} /> : null}
                      {link ? (
                        <Link route={{ name: "table", source, table: link.table }} search={link.search}>
                          <LinkIcon /> {link.label}
                        </Link>
                      ) : null}
                    </dd>
                  </div>
                );
              })}
            </dl>
          ) : null}

          {refs.error ? <ErrorNotice error={refs.error} onRetry={refs.reload} /> : null}
          <Links source={source} title="Referenced by" links={refLinks} />
          {onDelete && keyed ? (
            <div className="drawer-section">
              <button type="button" className="button button-danger" onClick={() => onDelete(keyed)}>
                Delete this row…
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </dialog>
  );
}
