import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { apiPath, apiSend } from "../api/client";
import { useApi } from "../api/useApi";
import { CloseIcon } from "../components/icons";
import { ErrorNotice, Loading } from "../components/Notices";
import { TypedConfirm } from "../components/TypedConfirm";
import { useModal } from "../components/useModal";
import { confirmId, parseDoc } from "./docs";
import { parseJSON } from "./json";
import { JsonTree } from "./JsonTree";

interface DocResponse {
  id: string;
  json: string;
}

export interface DocumentDrawerProps {
  source: string;
  collection: string;
  /** The _id as canonical Extended JSON; null when the drawer is closed. */
  id: string | null;
  write: boolean;
  onClose: () => void;
  /** The document was replaced or deleted. */
  onChanged: (what: "replaced" | "deleted") => void;
}

type Pane = "tree" | "raw";

function problemOf(text: string): string | null {
  const r = parseJSON(text);
  if (!r.ok) {
    return `Not valid JSON: ${r.error.message} (line ${r.error.line}, column ${r.error.column}).`;
  }
  if (r.value === null || typeof r.value !== "object" || Array.isArray(r.value)) {
    return "A document is one JSON object.";
  }
  return null;
}

/**
 * DocumentDrawer shows one document in full, as a collapsible tree or as the
 * raw Extended JSON. With --write it also edits the whole document and
 * deletes it; each of those asks for the _id to be typed.
 */
export function DocumentDrawer({ source, collection, id, write, onClose, onChanged }: DocumentDrawerProps): ReactElement {
  const dialog = useModal(id !== null, onClose);
  const doc = useApi<DocResponse>(id !== null ? apiPath("sources", source, "collections", collection, "documents", id) : null);
  const [pane, setPane] = useState<Pane>("tree");
  const [editing, setEditing] = useState<string | null>(null);
  const [replaceOpen, setReplaceOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const titleId = useId();
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    setPane("tree");
    setEditing(null);
    setReplaceOpen(false);
    setDeleteOpen(false);
  }, [id]);

  const parsed = useMemo(() => (doc.data ? parseDoc(doc.data.json) : null), [doc.data]);
  const problem = editing === null ? null : problemOf(editing);
  const expected = id === null ? "" : confirmId(id);

  return (
    <>
      <dialog
        ref={dialog}
        className="drawer"
        aria-labelledby={titleId}
        onClose={onClose}
        onClick={(e) => {
          if (e.target === dialog.current) {
            dialog.current?.close();
          }
        }}
      >
        <header className="drawer-header">
          <h2 id={titleId} className="drawer-title">
            {collection} {expected}
          </h2>
          <button type="button" className="icon-button" aria-label="Close the document" onClick={() => dialog.current?.close()}>
            <CloseIcon />
          </button>
        </header>
        <div className="drawer-body">
          {doc.loading ? <Loading what="the document" /> : null}
          {doc.error ? <ErrorNotice error={doc.error} onRetry={doc.reload} /> : null}
          {doc.data && editing === null ? (
            <>
              <div className="drawer-tools" role="group" aria-label="View">
                <button type="button" className="button button-small" aria-pressed={pane === "tree"} onClick={() => setPane("tree")}>
                  Tree
                </button>
                <button type="button" className="button button-small" aria-pressed={pane === "raw"} onClick={() => setPane("raw")}>
                  Extended JSON
                </button>
                {write ? (
                  <>
                    <button type="button" className="button button-small" onClick={() => setEditing(doc.data?.json ?? "")}>
                      Edit document
                    </button>
                    <button type="button" className="button button-small button-danger" onClick={() => setDeleteOpen(true)}>
                      Delete document…
                    </button>
                  </>
                ) : null}
              </div>
              {pane === "tree" && parsed ? (
                <JsonTree value={parsed} label="Document" />
              ) : (
                <pre className="drawer-value" tabIndex={0} aria-label="Document as Extended JSON">
                  {doc.data.json}
                </pre>
              )}
            </>
          ) : null}
          {editing !== null ? (
            <div className="doc-edit">
              <p className="muted">
                You are replacing the whole document. Its _id must stay <span className="mono">{expected}</span>.
              </p>
              <label htmlFor={`${titleId}-edit`}>Document (Extended JSON)</label>
              <textarea
                id={`${titleId}-edit`}
                ref={area}
                className="dialog-text doc-textarea"
                rows={18}
                spellCheck={false}
                value={editing}
                aria-invalid={problem ? true : undefined}
                aria-describedby={problem ? `${titleId}-problem` : undefined}
                onChange={(e) => setEditing(e.target.value)}
              />
              {problem ? (
                <p id={`${titleId}-problem`} className="editor-error" role="alert">
                  {problem}
                </p>
              ) : null}
              <div className="dialog-actions">
                <button type="button" className="button" onClick={() => setEditing(null)}>
                  Cancel
                </button>
                <button type="button" className="button button-primary" disabled={problem !== null} onClick={() => setReplaceOpen(true)}>
                  Replace document…
                </button>
              </div>
            </div>
          ) : null}
        </div>
      </dialog>

      <TypedConfirm
        open={replaceOpen}
        title="Replace this document?"
        what="the _id"
        expected={expected}
        confirmLabel="Replace document"
        onClose={() => setReplaceOpen(false)}
        onConfirm={async () => {
          if (id === null || editing === null) return;
          await apiSend("PUT", apiPath("sources", source, "collections", collection, "documents", id), { doc: editing });
          setReplaceOpen(false);
          setEditing(null);
          onChanged("replaced");
          doc.reload();
        }}
      >
        <p>
          This replaces the whole document of <span className="mono">{collection}</span> with the text you wrote. It is permanent: Studio has no undo.
        </p>
      </TypedConfirm>

      <TypedConfirm
        open={deleteOpen}
        title="Delete this document?"
        what="the _id"
        danger
        expected={expected}
        confirmLabel="Delete document"
        onClose={() => setDeleteOpen(false)}
        onConfirm={async (): Promise<void> => {
          if (id === null) return;
          await apiSend("DELETE", apiPath("sources", source, "collections", collection, "documents", id), { confirm: expected });
          setDeleteOpen(false);
          onChanged("deleted");
          dialog.current?.close();
        }}
      >
        <p>
          This deletes the document of <span className="mono">{collection}</span> with this _id. It is permanent: Studio has no undo.
        </p>
      </TypedConfirm>
    </>
  );
}
