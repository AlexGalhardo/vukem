import { useId, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import { ApiError, apiGet, apiPath, apiSend } from "../api/client";
import type { ApiCell, Column } from "../api/types";
import { ConfirmDialog } from "../console/ConfirmDialog";
import type { ConfirmRequest } from "../console/ConfirmDialog";
import { ErrorNotice, Loading } from "../components/Notices";
import { TypedConfirm } from "../components/TypedConfirm";
import { useModal } from "../components/useModal";
import { LiveRegion, useAnnouncer } from "../grid/Announcer";
import { copyText } from "../grid/clipboard";
import { emptyView } from "../grid/filters";
import { Grid } from "../grid/Grid";
import { useStreamed } from "../grid/useStreamed";
import { formatTTL } from "../lib/mode";
import { bytesOfEscaped, hexRows, hexText } from "./hex";

interface KeyResponse {
  key: string;
  type: string;
  ttlSeconds: number;
  size: number;
  fields: [string, string][];
  more: boolean;
  binary?: boolean;
}

interface Meta {
  type: string;
  ttlSeconds: number;
  size: number;
  binary: boolean;
}

/** The two columns each type shows, from what the server's Fields hold for it. */
export const typeColumns: Record<string, [string, string]> = {
  hash: ["Field", "Value"],
  list: ["Index", "Value"],
  set: ["Index", "Member"],
  zset: ["Member", "Score"],
  stream: ["Id", "Fields"],
};

const cutAt = 300;

function cell(t: string): ApiCell {
  return t.length > cutAt ? { t: t.slice(0, cutAt), x: true } : { t };
}

function gridColumns(type: string): Column[] {
  return (typeColumns[type] ?? ["Name", "Value"]).map((name) => ({ name, type: "", nullable: false }));
}

function messageOf(err: unknown): string {
  return err instanceof ApiError ? err.message : "Studio could not be reached.";
}

function ValueDialog({
  title,
  value,
  open,
  onClose,
  onEdit,
}: {
  title: string;
  value: string;
  open: boolean;
  onClose: () => void;
  /** Present for a hash in write mode: edit this value. */
  onEdit?: () => void;
}): ReactElement {
  const dialog = useModal(open, onClose);
  const id = useId();
  return (
    <dialog ref={dialog} className="dialog dialog-wide" aria-labelledby={`${id}-title`}>
      {open ? (
        <>
          <h2 id={`${id}-title`} className="dialog-title mono">
            {title}
          </h2>
          <pre className="drawer-value" tabIndex={0} aria-label="Value">
            {value}
          </pre>
          <div className="dialog-actions">
            <button type="button" className="button" onClick={() => void copyText(value)}>
              Copy
            </button>
            {onEdit ? (
              <button type="button" className="button" onClick={onEdit}>
                Edit value…
              </button>
            ) : null}
            <button type="button" className="button button-primary" onClick={onClose}>
              Close
            </button>
          </div>
        </>
      ) : null}
    </dialog>
  );
}

function TextEditDialog({
  open,
  title,
  initial,
  onClose,
  onSave,
}: {
  open: boolean;
  title: string;
  initial: string;
  onClose: () => void;
  onSave: (text: string) => void;
}): ReactElement {
  const dialog = useModal(open, onClose);
  const [text, setText] = useState(initial);
  const [seen, setSeen] = useState(initial);
  const id = useId();
  if (initial !== seen) {
    setSeen(initial);
    setText(initial);
  }
  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    onSave(text);
  };
  return (
    <dialog ref={dialog} className="dialog dialog-wide" aria-labelledby={`${id}-title`}>
      {open ? (
        <>
          <h2 id={`${id}-title`} className="dialog-title">
            {title}
          </h2>
          <form onSubmit={submit}>
            <label htmlFor={`${id}-value`}>Value</label>
            <textarea id={`${id}-value`} className="dialog-text doc-textarea" rows={10} spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} />
            <div className="dialog-actions">
              <button type="button" className="button" onClick={onClose}>
                Cancel
              </button>
              <button type="submit" className="button button-primary">
                Review the change…
              </button>
            </div>
          </form>
        </>
      ) : null}
    </dialog>
  );
}

function TTLDialog({
  open,
  current,
  onClose,
  onSave,
  busy,
  failure,
}: {
  open: boolean;
  current: number;
  onClose: () => void;
  onSave: (seconds: number) => void;
  busy: boolean;
  failure: string | null;
}): ReactElement {
  const dialog = useModal(open, onClose);
  const [text, setText] = useState("");
  const id = useId();
  const seconds = /^\d+$/.test(text.trim()) ? Number(text.trim()) : null;
  return (
    <dialog ref={dialog} className="dialog" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="dialog-title">
        Expiry
      </h2>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (seconds !== null && seconds > 0) onSave(seconds);
        }}
      >
        <p>{current < 0 ? "This key does not expire." : `This key expires in ${formatTTL(current)}.`}</p>
        <label htmlFor={`${id}-seconds`}>Expire after this many seconds</label>
        <input id={`${id}-seconds`} className="dialog-input" inputMode="numeric" value={text} autoComplete="off" onChange={(e) => setText(e.target.value)} />
        {failure ? (
          <p className="editor-error" role="alert">
            {failure}
          </p>
        ) : null}
        <div className="dialog-actions">
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="button" disabled={busy || current < 0} onClick={() => onSave(0)}>
            Remove the expiry
          </button>
          <button type="submit" className="button button-primary" disabled={busy || seconds === null || seconds <= 0}>
            Set the expiry
          </button>
        </div>
      </form>
    </dialog>
  );
}

function StringValue({ text, binary, size }: { text: string; binary: boolean; size: number }): ReactElement {
  const [view, setView] = useState<"text" | "hex">(binary ? "hex" : "text");
  const dump = useMemo(() => {
    if (!binary) return null;
    const { bytes, cut } = bytesOfEscaped(text);
    return { rows: hexRows(bytes), cut };
  }, [binary, text]);

  return (
    <div className="string-value">
      {binary ? (
        <div role="group" aria-label="View" className="toggle-group">
          <button type="button" className="button button-small" aria-pressed={view === "text"} onClick={() => setView("text")}>
            Text
          </button>
          <button type="button" className="button button-small" aria-pressed={view === "hex"} onClick={() => setView("hex")}>
            Hex
          </button>
        </div>
      ) : null}
      {binary ? <p className="muted">This value is not valid UTF-8, so it is binary. The text view writes each byte that is not printable ASCII as a backslash, x and two hex digits.</p> : null}
      {view === "hex" && dump ? (
        <>
          <table className="data-table hex-table" aria-label="Hex view">
            <thead>
              <tr>
                <th scope="col">Offset</th>
                <th scope="col">Bytes</th>
                <th scope="col">ASCII</th>
              </tr>
            </thead>
            <tbody>
              {dump.rows.slice(0, 256).map((r) => (
                <tr key={r.offset}>
                  <th scope="row" className="mono">
                    {r.offset}
                  </th>
                  <td className="mono">{r.hex}</td>
                  <td className="mono">{r.ascii}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {dump.rows.length > 256 ? <p className="muted">The dump shows the first 4,096 bytes. Copy holds all of what Studio read.</p> : null}
          {dump.cut ? <p className="muted">Studio reads at most 64 KiB of a value.</p> : null}
          <button type="button" className="button button-small" onClick={() => void copyText(hexText(dump.rows))}>
            Copy the hex
          </button>
        </>
      ) : (
        <pre className="drawer-value" tabIndex={0} aria-label="Value">
          {text}
        </pre>
      )}
      <p className="muted">{size.toLocaleString("en-US")} bytes.</p>
    </div>
  );
}

/**
 * KeyView shows one Redis key by its type: a string as text or hex, the other
 * types as a two-column grid read a page at a time. With --write it edits a
 * string or a hash field, sets or clears the expiry and deletes the key (the
 * key typed to confirm); the server audits each of them without values.
 */
export function KeyView({ source, keyName, write, onDeleted }: { source: string; keyName: string; write: boolean; onDeleted: () => void }): ReactElement {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [nonce, setNonce] = useState(0);
  const [missing, setMissing] = useState(false);
  const { message, announce } = useAnnouncer();
  const full = useRef(new WeakMap<ApiCell[], [string, string]>());
  const [shown, setShown] = useState<{ field: string; value: string } | null>(null);
  const [editField, setEditField] = useState<{ field: string; value: string } | null>(null);
  const [editString, setEditString] = useState(false);
  const [confirm, setConfirm] = useState<{ request: ConfirmRequest; run: () => Promise<void> } | null>(null);
  const [ttlOpen, setTtlOpen] = useState(false);
  const [ttlBusy, setTtlBusy] = useState(false);
  const [ttlFailure, setTtlFailure] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const streamed = useStreamed({
    resetKey: `${source}|${keyName}|${nonce}`,
    auto: false,
    fetchChunk: async (_cursor, step, signal) => {
      try {
        const res = await apiGet<KeyResponse>(apiPath("sources", source, "keys", keyName) + `?page=${step + 1}`, signal);
        if (step === 0) {
          setMissing(false);
          setMeta({ type: res.type, ttlSeconds: res.ttlSeconds, size: res.size, binary: res.binary === true });
        }
        const rows = res.fields.map((f) => {
          const cells = [cell(f[0]), cell(f[1])];
          full.current.set(cells, f);
          return cells;
        });
        return { rows, next: res.more ? String(step + 2) : null, hasMore: res.more };
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          setMissing(true);
          setMeta(null);
        }
        throw err;
      }
    },
  });

  const columns = useMemo(() => gridColumns(meta?.type ?? ""), [meta?.type]);
  const reload = (): void => setNonce((n) => n + 1);
  const keyPath = apiPath("sources", source, "keys", keyName);
  const stringValue = meta?.type === "string" ? (streamed.rows[0] ? (full.current.get(streamed.rows[0])?.[1] ?? "") : "") : "";
  const canEditString = write && meta?.type === "string" && !meta.binary;

  const change = async (body: unknown, done: string): Promise<void> => {
    await apiSend("PATCH", keyPath, body);
    announce(done);
    reload();
  };

  const review = (request: ConfirmRequest, run: () => Promise<void>): void => setConfirm({ request, run });

  const missingNotice = (
    <div className="notice" role="alert">
      <p>The key {keyName} does not exist. It may have expired or been deleted.</p>
    </div>
  );

  return (
    <section className="key-view" aria-label={`Key ${keyName}`}>
      {missing ? missingNotice : null}
      {streamed.data.error && !missing ? <ErrorNotice error={streamed.data.error} onRetry={streamed.data.retry} /> : null}
      {!meta && !missing && !streamed.data.error ? <Loading what="the key" /> : null}
      {failure ? (
        <p className="editor-error" role="alert">
          {failure}
        </p>
      ) : null}

      {meta ? (
        <>
          <dl className="key-facts">
            <div>
              <dt>Type</dt>
              <dd>
                <span className={`badge key-type key-type-${meta.type}`}>{meta.type}</span>
              </dd>
            </div>
            <div>
              <dt>Expires in</dt>
              <dd>{formatTTL(meta.ttlSeconds)}</dd>
            </div>
            <div>
              <dt>{meta.type === "string" ? "Size" : "Elements"}</dt>
              <dd>{meta.size.toLocaleString("en-US")}{meta.type === "string" ? " bytes" : ""}</dd>
            </div>
          </dl>

          {write ? (
            <div className="table-toolbar">
              {canEditString ? (
                <button type="button" className="button button-small" onClick={() => setEditString(true)}>
                  Edit value…
                </button>
              ) : null}
              <button type="button" className="button button-small" onClick={() => { setTtlFailure(null); setTtlOpen(true); }}>
                Expiry…
              </button>
              <button type="button" className="button button-small button-danger" onClick={() => setDeleteOpen(true)}>
                Delete key…
              </button>
              {meta.type === "string" && meta.binary ? <span className="muted">A binary value cannot be edited as text.</span> : null}
            </div>
          ) : null}

          {meta.type === "string" ? (
            <StringValue text={stringValue} binary={meta.binary} size={meta.size} />
          ) : (
            <>
              <p role="status" data-testid="key-count">
                Showing {streamed.loaded.toLocaleString("en-US")} of {meta.size.toLocaleString("en-US")} {meta.size === 1 ? "element" : "elements"}.
              </p>
              <div className="key-grid">
              <Grid
                source=""
                label={`${keyName} ${meta.type}`}
                columns={columns}
                primaryKey={[]}
                data={streamed.data}
                view={emptyView}
                density="normal"
                announce={announce}
                resetKey={`${keyName}|${nonce}`}
                onOpenRow={(i) => {
                  const row = streamed.rows[i];
                  const both = row ? full.current.get(row) : undefined;
                  if (both) setShown({ field: both[0], value: both[1] });
                }}
                onSort={() => announce("Elements are shown in the order the server returns them.")}
                onSetSort={() => undefined}
              />
              </div>
              <div className="table-toolbar">
                <button type="button" className="button" onClick={streamed.loadMore} disabled={!streamed.hasMore || streamed.loadingMore}>
                  {streamed.loadingMore ? "Loading…" : "Load more"}
                </button>
                <span className="muted">{streamed.hasMore ? "More elements exist." : "That is every element."}</span>
              </div>
            </>
          )}
        </>
      ) : null}

      <ValueDialog
        title={shown ? `${shown.field}` : ""}
        value={shown?.value ?? ""}
        open={shown !== null}
        onClose={() => setShown(null)}
        onEdit={
          write && meta?.type === "hash" && shown
            ? () => {
                setEditField(shown);
                setShown(null);
              }
            : undefined
        }
      />

      {write ? (
        <>
          <TextEditDialog
            open={editString}
            title={`Edit ${keyName}`}
            initial={stringValue}
            onClose={() => setEditString(false)}
            onSave={(text) =>
              review(
                { title: "Replace this value?", message: `This sets the string ${keyName} to the text you wrote and keeps its expiry. It is permanent: Studio has no undo.`, confirmLabel: "Replace value" },
                async () => {
                  await change({ action: "set", value: text }, "Value saved.");
                  setEditString(false);
                },
              )
            }
          />
          <TextEditDialog
            open={editField !== null}
            title={editField ? `Edit field ${editField.field}` : ""}
            initial={editField?.value ?? ""}
            onClose={() => setEditField(null)}
            onSave={(text) => {
              const field = editField?.field ?? "";
              review(
                { title: "Replace this field?", message: `This sets the field ${field} of ${keyName} to the text you wrote. It is permanent: Studio has no undo.`, confirmLabel: "Replace field" },
                async () => {
                  await change({ action: "hset", field, value: text }, "Field saved.");
                  setEditField(null);
                },
              );
            }}
          />
          <TTLDialog
            open={ttlOpen}
            current={meta?.ttlSeconds ?? -1}
            busy={ttlBusy}
            failure={ttlFailure}
            onClose={() => setTtlOpen(false)}
            onSave={(seconds) => {
              setTtlBusy(true);
              change({ action: "ttl", seconds }, seconds === 0 ? "Expiry removed." : "Expiry set.").then(
                () => {
                  setTtlBusy(false);
                  setTtlOpen(false);
                },
                (err: unknown) => {
                  setTtlBusy(false);
                  setTtlFailure(messageOf(err));
                },
              );
            }}
          />
          <ConfirmDialog
            request={confirm?.request ?? null}
            onCancel={() => setConfirm(null)}
            onAfterClose={() => undefined}
            onConfirm={() => {
              const run = confirm?.run;
              setConfirm(null);
              run?.().catch((err: unknown) => setFailure(messageOf(err)));
            }}
          />
          <TypedConfirm
            open={deleteOpen}
            title="Delete this key?"
            what="the key"
            danger
            expected={keyName}
            confirmLabel="Delete key"
            onClose={() => setDeleteOpen(false)}
            onConfirm={async () => {
              await apiSend("DELETE", keyPath, { confirm: keyName });
              setDeleteOpen(false);
              onDeleted();
            }}
          >
            <p>This deletes the key and its value. It is permanent: Studio has no undo.</p>
          </TypedConfirm>
        </>
      ) : null}
      <LiveRegion message={message} />
    </section>
  );
}
