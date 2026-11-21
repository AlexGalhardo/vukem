import { describe, expect, test } from "bun:test";
import type { ApiCell, Column } from "../api/types";
import { buildInsert, canEdit, cellOfDraft, editReducer, patchFor, validateDraft } from "./editState";
import type { At, EditAction, EditContext, EditState } from "./editState";

const at: At = { row: 3, col: 2, key: ["7"], column: "name", kind: "text", nullable: true };
const ctx: EditContext = {
  writable: true,
  hasPrimaryKey: true,
  isPrimaryKey: false,
  kind: "text",
  cell: { t: "ada" },
};
const idle: EditState = { phase: "idle" };

function run(actions: EditAction[], from: EditState = idle): EditState {
  return actions.reduce(editReducer, from);
}

const editing = (original: ApiCell = { t: "ada" }): EditState =>
  run([{ type: "start", at, ctx }, { type: "loaded", cell: original }]);

describe("canEdit", () => {
  test("a writable cell of a keyed table may be edited", () => {
    expect(canEdit(ctx)).toEqual({ ok: true });
  });

  test("refuses with a reason in read-only mode, without a key, on a key column and on binary", () => {
    const reasons = [
      canEdit({ ...ctx, writable: false }),
      canEdit({ ...ctx, hasPrimaryKey: false }),
      canEdit({ ...ctx, isPrimaryKey: true }),
      canEdit({ ...ctx, kind: "binary" }),
      canEdit({ ...ctx, cell: { t: "<binary 2 bytes>", b: true } }),
    ];
    for (const r of reasons) {
      expect(r.ok).toBe(false);
      expect(r.ok === false && r.reason.length > 10).toBe(true);
    }
    expect(JSON.stringify(reasons[0])).toContain("read-only");
    expect(JSON.stringify(reasons[2])).toContain("primary key");
  });
});

describe("starting", () => {
  test("a cell goes to loading first: the grid cell may be cut, so the full value is read", () => {
    expect(run([{ type: "start", at, ctx: { ...ctx, cell: { t: "a".repeat(200), x: true } } }]).phase).toBe("loading");
  });

  test("a refused cell stays idle and says why", () => {
    const state = run([{ type: "start", at, ctx: { ...ctx, isPrimaryKey: true } }]);
    expect(state.phase).toBe("idle");
    expect(state.phase === "idle" && state.notice).toContain("primary key");
  });

  test("read-only mode never starts an edit, whatever the cell", () => {
    for (const cell of [{ t: "x" }, { t: "", n: true }, { t: "1", l: true }]) {
      expect(run([{ type: "start", at, ctx: { ...ctx, writable: false, cell } }]).phase).toBe("idle");
    }
  });

  test("a full value that is binary or over 64 KiB is refused after it loads", () => {
    const base: EditAction[] = [{ type: "start", at, ctx }];
    const binary = run([...base, { type: "loaded", cell: { t: "<binary 3 bytes>", b: true } }]);
    const huge = run([...base, { type: "loaded", cell: { t: "x".repeat(10), x: true } }]);
    expect(binary.phase === "idle" && binary.notice).toContain("Binary");
    expect(huge.phase === "idle" && huge.notice).toContain("64 KiB");
  });

  test("a failed load goes back to idle with the message", () => {
    const state = run([{ type: "start", at, ctx }, { type: "loadFailed", message: "gone" }]);
    expect(state).toEqual({ phase: "idle", notice: "gone" });
  });

  test("the editor starts from the full value, which is also the expected value", () => {
    const state = editing({ t: "full value, beyond the 200 characters of the cell" });
    expect(state.phase === "editing" && state.original).toEqual({
      text: "full value, beyond the 200 characters of the cell",
      null: false,
    });
  });

  test("a second start while editing is ignored", () => {
    const state = run([{ type: "start", at: { ...at, col: 4 }, ctx }], editing());
    expect(state.phase === "editing" && state.at.col).toBe(2);
  });
});

describe("commit", () => {
  test("sends the new value with the previous one as expected", () => {
    const state = run([{ type: "input", text: "grace" }, { type: "commit" }], editing());
    expect(state.phase).toBe("saving");
    if (state.phase === "saving") {
      expect(patchFor(state)).toEqual({
        fields: { name: { text: "grace", null: false } },
        expect: { name: { text: "ada", null: false } },
      });
      expect(state.next).toBe("stay");
    }
  });

  test("an unchanged value sends nothing", () => {
    expect(run([{ type: "commit" }], editing()).phase).toBe("idle");
    expect(run([{ type: "input", text: "grace" }, { type: "input", text: "ada" }, { type: "commit" }], editing()).phase).toBe(
      "idle",
    );
  });

  test("Tab commits and asks to move right", () => {
    const state = run([{ type: "input", text: "x" }, { type: "commit", next: "right" }], editing());
    expect(state.phase === "saving" && state.next).toBe("right");
  });

  test("saved returns to idle", () => {
    const state = run([{ type: "input", text: "x" }, { type: "commit" }, { type: "saved" }], editing());
    expect(state).toEqual(idle);
  });

  test("an invalid value stays in the editor with a message and sends nothing", () => {
    const numberAt: At = { ...at, kind: "number", column: "qty", nullable: false };
    const start = run([{ type: "start", at: numberAt, ctx: { ...ctx, kind: "number" } }, { type: "loaded", cell: { t: "5", l: true } }]);
    const state = run([{ type: "input", text: "five" }, { type: "commit" }], start);
    expect(state.phase === "editing" && state.error).toBe("Enter a number.");
  });
});

describe("cancel", () => {
  test("Escape drops the draft", () => {
    expect(run([{ type: "input", text: "x" }, { type: "cancel" }], editing())).toEqual(idle);
  });

  test("it cannot cancel a save already sent", () => {
    const saving = run([{ type: "input", text: "x" }, { type: "commit" }], editing());
    expect(run([{ type: "cancel" }], saving)).toEqual(saving);
  });
});

describe("NULL", () => {
  test("Set to NULL is sent as null with empty text", () => {
    const state = run([{ type: "setNull" }, { type: "commit" }], editing());
    expect(state.phase === "saving" && patchFor(state).fields).toEqual({ name: { text: "", null: true } });
  });

  test("a NULL cell loads as NULL and typing replaces it", () => {
    const state = editing({ t: "", n: true });
    expect(state.phase === "editing" && state.draft).toEqual({ text: "", null: true });
    const typed = run([{ type: "input", text: "x" }, { type: "commit" }], state);
    expect(typed.phase === "saving" && patchFor(typed).expect).toEqual({ name: { text: "", null: true } });
  });

  test("a column that does not accept NULL refuses it with a message", () => {
    const notNull: At = { ...at, nullable: false };
    const start = run([{ type: "start", at: notNull, ctx }, { type: "loaded", cell: { t: "ada" } }]);
    const state = run([{ type: "setNull" }], start);
    expect(state.phase === "editing" && state.error).toContain("does not accept NULL");
    expect(state.phase === "editing" && state.draft.null).toBe(false);
  });

  test("NULL stays NULL: no change, no request", () => {
    expect(run([{ type: "commit" }], editing({ t: "", n: true })).phase).toBe("idle");
  });
});

describe("conflict and rollback", () => {
  const saving = (): EditState => run([{ type: "input", text: "mine" }, { type: "commit", next: "right" }], editing());

  test("409 with the current value offers keep mine or take theirs", () => {
    const state = run([{ type: "rejected", status: 409, message: "changed", current: { t: "theirs" } }], saving());
    expect(state.phase).toBe("conflict");
    expect(state.phase === "conflict" && state.mine).toEqual({ text: "mine", null: false });
    expect(state.phase === "conflict" && state.theirs).toEqual({ text: "theirs", null: false });
  });

  test("keep mine retries with their value as the expected one", () => {
    const state = run(
      [{ type: "rejected", status: 409, message: "changed", current: { t: "theirs" } }, { type: "keepMine" }],
      saving(),
    );
    expect(state.phase).toBe("saving");
    if (state.phase === "saving") {
      expect(patchFor(state)).toEqual({
        fields: { name: { text: "mine", null: false } },
        expect: { name: { text: "theirs", null: false } },
      });
      expect(state.next).toBe("right");
    }
  });

  test("take theirs drops mine", () => {
    const state = run(
      [{ type: "rejected", status: 409, message: "changed", current: { t: "", n: true } }, { type: "takeTheirs" }],
      saving(),
    );
    expect(state).toEqual(idle);
  });

  test("another error rolls back: the editor reopens with the reason and the original value", () => {
    const state = run([{ type: "rejected", status: 422, message: "name: expected a whole number" }], saving());
    expect(state.phase).toBe("editing");
    if (state.phase === "editing") {
      expect(state.error).toBe("name: expected a whole number");
      expect(state.original).toEqual({ text: "ada", null: false });
      expect(state.draft.text).toBe("mine");
    }
  });

  test("a 500 is a rollback too, with the generic message", () => {
    const state = run([{ type: "rejected", status: 500, message: "The database could not run the request." }], saving());
    expect(state.phase === "editing" && state.error).toContain("database");
  });

  test("a rejection while not saving is ignored", () => {
    expect(run([{ type: "rejected", status: 500, message: "x" }], idle)).toEqual(idle);
  });
});

describe("validateDraft and cells", () => {
  const kinds = (kind: At["kind"]): Pick<At, "kind" | "nullable" | "column"> => ({ kind, nullable: true, column: "c" });

  test("numbers, booleans, JSON and dates", () => {
    expect(validateDraft(kinds("number"), { text: " -12.5e3 ", null: false })).toBeNull();
    expect(validateDraft(kinds("number"), { text: "1,5", null: false })).not.toBeNull();
    expect(validateDraft(kinds("boolean"), { text: "TRUE", null: false })).toBeNull();
    expect(validateDraft(kinds("boolean"), { text: "yes", null: false })).not.toBeNull();
    expect(validateDraft(kinds("json"), { text: '{"a":1}', null: false })).toBeNull();
    expect(validateDraft(kinds("json"), { text: "{a:1}", null: false })).not.toBeNull();
    expect(validateDraft(kinds("date"), { text: "2026-01-02", null: false })).toBeNull();
    expect(validateDraft(kinds("date"), { text: "  ", null: false })).not.toBeNull();
    expect(validateDraft(kinds("text"), { text: "", null: false })).toBeNull();
  });

  test("a draft as a grid cell", () => {
    expect(cellOfDraft({ text: "", null: true }, "text")).toEqual({ t: "", n: true });
    expect(cellOfDraft({ text: " 5 ", null: false }, "number")).toEqual({ t: "5", l: true });
    expect(cellOfDraft({ text: '{ "a": 1 }', null: false }, "json")).toEqual({ t: '{"a":1}' });
    expect(cellOfDraft({ text: "hi", null: false }, "text")).toEqual({ t: "hi" });
  });
});

describe("new row", () => {
  const columns: Column[] = [
    { name: "id", type: "integer", nullable: false },
    { name: "name", type: "text", nullable: false },
    { name: "qty", type: "integer", nullable: false, default: "1" },
    { name: "note", type: "text", nullable: true },
  ];

  test("default columns are left out of the body", () => {
    const plan = buildInsert(columns, { name: { mode: "value", text: "bolt" } }, ["id"]);
    expect(plan.errors).toEqual({});
    expect(plan.body).toEqual({ fields: { name: { text: "bolt", null: false } } });
  });

  test("NULL is sent as null, values as typed", () => {
    const plan = buildInsert(
      columns,
      {
        id: { mode: "value", text: "9" },
        name: { mode: "value", text: "nut" },
        qty: { mode: "value", text: "4" },
        note: { mode: "null", text: "" },
      },
      ["id"],
    );
    expect(plan.body.fields).toEqual({
      id: { text: "9", null: false },
      name: { text: "nut", null: false },
      qty: { text: "4", null: false },
      note: { text: "", null: true },
    });
  });

  test("a required column left on default is reported, and so are bad values and NULL on a required column", () => {
    const plan = buildInsert(
      columns,
      { qty: { mode: "value", text: "many" }, name: { mode: "null", text: "" } },
      ["id"],
    );
    expect(Object.keys(plan.errors).sort()).toEqual(["name", "qty"]);
    expect(buildInsert(columns, {}, ["id"]).errors.name).toContain("no default");
  });

  test("an empty typed value is refused, because the server would read it as the default", () => {
    const plan = buildInsert(columns, { name: { mode: "value", text: "" } }, ["id"]);
    expect(plan.errors.name).toContain("Type a value");
  });

  test("the primary key may be left to the database", () => {
    expect(buildInsert(columns, { name: { mode: "value", text: "x" } }, ["id"]).errors.id).toBeUndefined();
  });
});
