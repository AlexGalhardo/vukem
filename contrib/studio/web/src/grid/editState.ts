import type { ApiCell, Column } from "../api/types";
import { columnKind } from "./filters";
import type { ColumnKind } from "./filters";
import { jsonCompact } from "./format";

/**
 * The state machine of one inline edit, free of the DOM and of the network so
 * a table of cases can check it. The grid feeds it events and carries out
 * what the state asks for: load the full value, send the patch, move on.
 */

/** The text of a field and whether it is NULL: the shape of the API's fields. */
export interface Draft {
  text: string;
  null: boolean;
}

/** The cell being edited. */
export interface At {
  row: number;
  col: number;
  key: string[];
  column: string;
  kind: ColumnKind;
  nullable: boolean;
}

export type EditState =
  | { phase: "idle"; notice?: string }
  /** The grid cell may be cut at 200 characters, so the full value is read first. */
  | { phase: "loading"; at: At }
  | { phase: "editing"; at: At; original: Draft; draft: Draft; error?: string }
  | { phase: "saving"; at: At; original: Draft; draft: Draft; expect: Draft; next: Move }
  /** Someone changed the row meanwhile: mine is what was typed, theirs is what the row holds now. */
  | { phase: "conflict"; at: At; mine: Draft; theirs: Draft; next: Move };

/** Where the focus goes after a commit. */
export type Move = "stay" | "right";

export interface EditContext {
  /** Studio was started with --write. */
  writable: boolean;
  hasPrimaryKey: boolean;
  isPrimaryKey: boolean;
  kind: ColumnKind;
  /** The cell as the grid shows it (possibly cut). */
  cell: ApiCell;
}

/** Values over this many bytes are cut by the server in a row read, so they are not edited inline. */
export const oversizeBytes = 64 << 10;

export type CanEdit = { ok: true } | { ok: false; reason: string };

/** canEdit says whether a cell may be edited inline, and if not, why. */
export function canEdit(ctx: EditContext): CanEdit {
  if (!ctx.writable) {
    return { ok: false, reason: "Studio is read-only. Start it with --write to edit." };
  }
  if (!ctx.hasPrimaryKey) {
    return { ok: false, reason: "This table has no primary key, so its rows cannot be edited." };
  }
  if (ctx.isPrimaryKey) {
    return { ok: false, reason: "A primary key column cannot be edited: it names the row." };
  }
  if (ctx.kind === "binary" || ctx.cell.b) {
    return { ok: false, reason: "Binary values cannot be edited in the grid." };
  }
  return { ok: true };
}

/** draftOf is the editable form of a full cell. */
export function draftOf(cell: ApiCell): Draft {
  return cell.n ? { text: "", null: true } : { text: cell.t, null: false };
}

export type EditAction =
  | { type: "start"; at: At; ctx: EditContext }
  | { type: "loaded"; cell: ApiCell }
  | { type: "loadFailed"; message: string }
  | { type: "input"; text: string }
  | { type: "setNull" }
  | { type: "commit"; next?: Move }
  | { type: "saved" }
  | { type: "rejected"; status: number; message: string; current?: ApiCell }
  | { type: "keepMine" }
  | { type: "takeTheirs" }
  | { type: "cancel" };

const numberRe = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;

/** validateDraft is the client-side check before a patch is sent; the server checks again. */
export function validateDraft(at: Pick<At, "kind" | "nullable" | "column">, draft: Draft): string | null {
  if (draft.null) {
    return at.nullable ? null : `${at.column} does not accept NULL.`;
  }
  const text = draft.text.trim();
  switch (at.kind) {
    case "number":
      return numberRe.test(text) ? null : "Enter a number.";
    case "boolean":
      return ["true", "false", "1", "0"].includes(text.toLowerCase()) ? null : "Choose true or false.";
    case "json":
      try {
        JSON.parse(draft.text);
        return null;
      } catch {
        return "Enter valid JSON.";
      }
    case "date":
      return text === "" ? "Enter a date, such as 2026-01-02 or 2026-01-02T03:04:05Z." : null;
    default:
      return null;
  }
}

/** sameDraft compares two drafts the way a user would: NULL against NULL, text against text. */
export function sameDraft(a: Draft, b: Draft): boolean {
  return a.null === b.null && (a.null || a.text === b.text);
}

const idle: EditState = { phase: "idle" };

/** editReducer moves the edit from one state to the next. An event that does not fit the state is ignored. */
export function editReducer(state: EditState, action: EditAction): EditState {
  switch (action.type) {
    case "start": {
      if (state.phase !== "idle") {
        return state;
      }
      const can = canEdit(action.ctx);
      return can.ok ? { phase: "loading", at: action.at } : { phase: "idle", notice: can.reason };
    }
    case "loaded": {
      if (state.phase !== "loading") {
        return state;
      }
      if (action.cell.b) {
        return { phase: "idle", notice: "Binary values cannot be edited in the grid." };
      }
      if (action.cell.x) {
        return { phase: "idle", notice: "This value is larger than 64 KiB and cannot be edited in the grid." };
      }
      const original = draftOf(action.cell);
      return { phase: "editing", at: state.at, original, draft: original };
    }
    case "loadFailed":
      return state.phase === "loading" ? { phase: "idle", notice: action.message } : state;
    case "input":
      return state.phase === "editing" ? { ...state, draft: { text: action.text, null: false }, error: undefined } : state;
    case "setNull":
      if (state.phase !== "editing") {
        return state;
      }
      return state.at.nullable
        ? { ...state, draft: { text: "", null: true }, error: undefined }
        : { ...state, error: `${state.at.column} does not accept NULL.` };
    case "commit": {
      if (state.phase !== "editing") {
        return state;
      }
      // Nothing changed: there is nothing to send, and the focus may still move on.
      if (sameDraft(state.original, state.draft)) {
        return idle;
      }
      const error = validateDraft(state.at, state.draft);
      if (error) {
        return { ...state, error };
      }
      return {
        phase: "saving",
        at: state.at,
        original: state.original,
        draft: state.draft,
        expect: state.original,
        next: action.next ?? "stay",
      };
    }
    case "saved":
      return state.phase === "saving" ? idle : state;
    case "rejected": {
      if (state.phase !== "saving") {
        return state;
      }
      if (action.status === 409 && action.current) {
        return { phase: "conflict", at: state.at, mine: state.draft, theirs: draftOf(action.current), next: state.next };
      }
      // Rollback: the cell shows what it held, and the editor stays open with the reason.
      return { phase: "editing", at: state.at, original: state.original, draft: state.draft, error: action.message };
    }
    case "keepMine":
      return state.phase === "conflict"
        ? {
            phase: "saving",
            at: state.at,
            original: state.theirs,
            draft: state.mine,
            expect: state.theirs,
            next: state.next,
          }
        : state;
    case "takeTheirs":
      return state.phase === "conflict" ? idle : state;
    case "cancel":
      return state.phase === "idle" || state.phase === "saving" ? state : idle;
  }
}

/** The body of the PATCH for a saving state: the new value and the value the row must still hold. */
export interface Patch {
  fields: Record<string, { text: string; null: boolean }>;
  expect: Record<string, { text: string; null: boolean }>;
}

/** patchFor builds the request body of a saving state. */
export function patchFor(state: Extract<EditState, { phase: "saving" }>): Patch {
  const wire = (d: Draft): { text: string; null: boolean } => ({ text: d.null ? "" : d.text, null: d.null });
  return { fields: { [state.at.column]: wire(state.draft) }, expect: { [state.at.column]: wire(state.expect) } };
}

/** cellOfDraft is how a draft looks in the grid while it is being saved, or after it was. */
export function cellOfDraft(draft: Draft, kind: ColumnKind): ApiCell {
  if (draft.null) {
    return { t: "", n: true };
  }
  if (kind === "json") {
    return { t: jsonCompact(draft.text) };
  }
  if (kind === "number" || kind === "boolean") {
    return { t: draft.text.trim(), l: true };
  }
  return { t: draft.text };
}

/** How one column of a new row is filled. */
export interface NewField {
  mode: "default" | "null" | "value";
  text: string;
}

export interface InsertBody {
  fields: Record<string, { text: string; null: boolean }>;
}

export interface InsertPlan {
  body: InsertBody;
  /** One message per column that cannot be sent as it is, by column name. */
  errors: Record<string, string>;
}

/**
 * buildInsert turns the form of a new row into the POST body. A column left on
 * "default" is not sent, so the database fills it; that is only possible for a
 * column that has a default, accepts NULL or is generated by the database.
 */
export function buildInsert(
  columns: readonly Column[],
  fields: Readonly<Record<string, NewField>>,
  primaryKey: readonly string[],
): InsertPlan {
  const out: InsertBody["fields"] = {};
  const errors: Record<string, string> = {};

  for (const column of columns) {
    const field = fields[column.name] ?? { mode: "default", text: "" };
    const kind = columnKindOf(column);

    if (field.mode === "default") {
      // A column with no default can still be left out when the database makes the value (a key) or accepts NULL.
      if (column.default === undefined && !column.nullable && !primaryKey.includes(column.name)) {
        errors[column.name] = `${column.name} has no default and cannot be NULL: give it a value.`;
      }
      continue;
    }

    if (field.mode === "value" && field.text === "") {
      // The server reads an empty text on insert as "use the default", so it cannot mean an empty string.
      errors[column.name] = `Type a value for ${column.name}, or choose the default or NULL.`;
      continue;
    }

    const draft: Draft = field.mode === "null" ? { text: "", null: true } : { text: field.text, null: false };
    const problem = validateDraft({ kind, nullable: column.nullable, column: column.name }, draft);
    if (problem) {
      errors[column.name] = problem;
      continue;
    }
    out[column.name] = { text: draft.null ? "" : draft.text, null: draft.null };
  }

  return { body: { fields: out }, errors };
}

/** columnContext builds the parts of an EditContext that come from the table definition. */
export function columnContext(
  column: Column,
  primaryKey: readonly string[],
): Pick<EditContext, "hasPrimaryKey" | "isPrimaryKey"> {
  return { hasPrimaryKey: primaryKey.length > 0, isPrimaryKey: primaryKey.includes(column.name) };
}

function columnKindOf(column: Column): ColumnKind {
  return columnKind(column.type);
}
