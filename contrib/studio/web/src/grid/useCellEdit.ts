import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { ApiError, apiGet, apiPath, apiSend } from "../api/client";
import type { ApiCell, RowResponse, TableDetail } from "../api/types";
import { cellOfDraft, editReducer, patchFor, sameDraft } from "./editState";
import type { At, EditState, Move } from "./editState";
import { columnKind } from "./filters";
import type { GridData } from "./useGridData";
import { keyParts, packKey } from "./rowKey";

export type CellStatus = "saving" | "saved" | "error";

export interface MoveRequest {
  row: number;
  col: number;
  nonce: number;
}

export interface CellEdit {
  /** Studio was started with --write and the table has a primary key. */
  enabled: boolean;
  state: EditState;
  /** The reason the last attempt to edit was refused, if it was. */
  notice: string | null;
  /** Begins editing the cell at a data row and a grid column (1 and up). */
  start: (row: number, col: number) => void;
  input: (text: string) => void;
  setNull: () => void;
  commit: (next?: Move) => void;
  cancel: () => void;
  keepMine: () => void;
  takeTheirs: () => void;
  /** What a cell shows while it is being saved, and its status flag. */
  display: (row: number, col: number) => { cell: ApiCell | null; status: CellStatus | null };
  /** After a Tab commit the grid moves its focus here. */
  moveRequest: MoveRequest | null;
}

interface Options {
  source: string;
  table: string;
  detail: TableDetail;
  /** bootstrap.write */
  write: boolean;
  data: GridData;
  announce: (text: string) => void;
}

function messageOf(err: unknown): string {
  return err instanceof ApiError ? err.message : "Studio could not be reached.";
}

/**
 * useCellEdit connects the edit state machine to the API: it reads the full
 * value before an editor opens, sends the guarded patch, shows the new value
 * at once, and takes it back when the server refuses.
 */
export function useCellEdit({ source, table, detail, write, data, announce }: Options): CellEdit {
  const [state, dispatch] = useReducer(editReducer, { phase: "idle" } as EditState);
  const [saved, setSaved] = useState<{ row: number; col: number } | null>(null);
  const [moveRequest, setMoveRequest] = useState<MoveRequest | null>(null);
  const sent = useRef<EditState | null>(null);
  const nonce = useRef(0);
  const enabled = write && detail.primaryKey.length > 0;

  const requestMove = useCallback((row: number, col: number) => {
    nonce.current++;
    setMoveRequest({ row, col, nonce: nonce.current });
  }, []);

  const loadingAt: At | null = state.phase === "loading" ? state.at : null;

  useEffect(() => {
    if (!loadingAt) {
      return;
    }
    const controller = new AbortController();
    apiGet<RowResponse>(apiPath("sources", source, "tables", table, "rows", packKey(loadingAt.key)), controller.signal).then(
      (res) => {
        const cell = res.cells[loadingAt.col - 1];
        if (cell) {
          dispatch({ type: "loaded", cell });
        } else {
          dispatch({ type: "loadFailed", message: "The row has no such column." });
        }
      },
      (err: unknown) => {
        if (!controller.signal.aborted) {
          dispatch({ type: "loadFailed", message: messageOf(err) });
        }
      },
    );
    return () => controller.abort();
  }, [loadingAt, source, table]);

  useEffect(() => {
    if (state.phase !== "saving" || sent.current === state) {
      return;
    }
    sent.current = state;
    const { at, draft, next } = state;
    apiSend("PATCH", apiPath("sources", source, "tables", table, "rows", packKey(at.key)), patchFor(state)).then(
      () => {
        dispatch({ type: "saved" });
        data.patchCell(at.row, at.col - 1, cellOfDraft(draft, at.kind));
        announce(`Saved ${at.column} of row ${at.row + 1}.`);
        setSaved({ row: at.row, col: at.col });
        if (next === "right") {
          requestMove(at.row, at.col + 1);
        }
      },
      (err: unknown) => {
        if (err instanceof ApiError) {
          dispatch({ type: "rejected", status: err.status, message: err.message, current: err.current?.[at.column] });
          announce(err.status === 409 ? `${at.column} changed in the database. Choose what to keep.` : `Not saved: ${err.message}`);
        } else {
          dispatch({ type: "rejected", status: 0, message: messageOf(err) });
          announce(`Not saved: ${messageOf(err)}`);
        }
      },
    );
  }, [state, source, table, data, announce, requestMove]);

  useEffect(() => {
    if (!saved) {
      return;
    }
    const t = window.setTimeout(() => setSaved(null), 2500);
    return () => window.clearTimeout(t);
  }, [saved]);

  const notice = state.phase === "idle" ? (state.notice ?? null) : null;
  useEffect(() => {
    if (notice) {
      announce(notice);
    }
  }, [notice, announce]);

  const start = useCallback(
    (row: number, col: number) => {
      const cells = data.row(row);
      const column = detail.columns[col - 1];
      if (!cells || !column || col < 1) {
        return;
      }
      const cell = cells[col - 1];
      const key = keyParts(detail.columns, detail.primaryKey, cells);
      if (!cell) {
        return;
      }
      const kind = columnKind(column.type);
      // The reducer decides and records the reason as a notice when the cell cannot be edited.
      dispatch({
        type: "start",
        at: { row, col, key: key ?? [], column: column.name, kind, nullable: column.nullable },
        ctx: {
          writable: write,
          hasPrimaryKey: key !== null,
          isPrimaryKey: detail.primaryKey.includes(column.name),
          kind,
          cell,
        },
      });
    },
    [data, detail, write],
  );

  const commit = useCallback(
    (next: Move = "stay") => {
      // An unchanged value sends nothing, but Tab still moves on.
      if (state.phase === "editing" && sameDraft(state.original, state.draft) && next === "right") {
        requestMove(state.at.row, state.at.col + 1);
      }
      dispatch({ type: "commit", next });
    },
    [state, requestMove],
  );

  const takeTheirs = useCallback(() => {
    if (state.phase === "conflict") {
      data.patchCell(state.at.row, state.at.col - 1, cellOfDraft(state.theirs, state.at.kind));
      announce(`Kept the value in the database for ${state.at.column}.`);
    }
    dispatch({ type: "takeTheirs" });
  }, [state, data, announce]);

  const display = useCallback(
    (row: number, col: number): { cell: ApiCell | null; status: CellStatus | null } => {
      if ((state.phase === "saving" || state.phase === "editing" || state.phase === "conflict") && state.at.row === row && state.at.col === col) {
        if (state.phase === "saving") {
          return { cell: cellOfDraft(state.draft, state.at.kind), status: "saving" };
        }
        return { cell: null, status: state.phase === "editing" && state.error ? "error" : null };
      }
      return { cell: null, status: saved && saved.row === row && saved.col === col ? "saved" : null };
    },
    [state, saved],
  );

  return {
    enabled,
    state,
    notice,
    start,
    input: (text) => dispatch({ type: "input", text }),
    setNull: () => dispatch({ type: "setNull" }),
    commit,
    cancel: () => dispatch({ type: "cancel" }),
    keepMine: () => dispatch({ type: "keepMine" }),
    takeTheirs,
    display,
    moveRequest,
  };
}
