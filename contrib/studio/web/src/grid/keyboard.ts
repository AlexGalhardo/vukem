/**
 * The keyboard model of the grid, kept free of the DOM so a table of key
 * sequences can check it.
 *
 * Positions: row -1 is the header row, rows 0 and up are data rows. Column 0
 * is the gutter (row number and checkbox), columns 1 and up are the data
 * columns.
 */

export interface Pos {
  row: number;
  col: number;
}

export interface Dims {
  /** Data rows (the estimate while the count is not known). */
  rows: number;
  /** All columns, the gutter included. */
  cols: number;
  /** Rows a page key moves by: the rows on screen. */
  page: number;
}

export interface KeyInput {
  key: string;
  ctrl?: boolean;
  meta?: boolean;
  shift?: boolean;
  alt?: boolean;
}

export type KeyAction =
  | { type: "none" }
  /** Move the focus; extend is true when Shift asks to select along the way. */
  | { type: "move"; to: Pos; extend: boolean }
  /** Enter or F2 on a cell. */
  | { type: "activate"; edit: boolean }
  | { type: "toggleSelect" }
  | { type: "copy" }
  | { type: "cancel" }
  | { type: "menu" }
  /** Ctrl+End asked for the very last row: the grid may need to read from the end. */
  | { type: "jumpEnd"; to: Pos };

const none: KeyAction = { type: "none" };

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(Math.max(n, lo), hi);
}

/** clampPos keeps a position inside the grid. */
export function clampPos(pos: Pos, dims: Dims): Pos {
  return { row: clamp(pos.row, -1, Math.max(dims.rows - 1, 0)), col: clamp(pos.col, 0, Math.max(dims.cols - 1, 0)) };
}

/** keyAction maps one key press at pos to what the grid should do. */
export function keyAction(pos: Pos, dims: Dims, input: KeyInput): KeyAction {
  const { key } = input;
  const ctrl = Boolean(input.ctrl || input.meta);
  const shift = Boolean(input.shift);
  const lastRow = Math.max(dims.rows - 1, 0);
  const lastCol = Math.max(dims.cols - 1, 0);
  const move = (row: number, col: number): KeyAction => ({
    type: "move",
    to: clampPos({ row, col }, dims),
    extend: shift && row >= 0 && pos.row >= 0,
  });

  if (input.alt) {
    return none;
  }

  switch (key) {
    case "ArrowDown":
      return ctrl ? move(lastRow, pos.col) : move(pos.row + 1, pos.col);
    case "ArrowUp":
      return ctrl ? move(-1, pos.col) : move(pos.row - 1, pos.col);
    case "ArrowRight":
      return ctrl ? move(pos.row, lastCol) : move(pos.row, pos.col + 1);
    case "ArrowLeft":
      return ctrl ? move(pos.row, 0) : move(pos.row, pos.col - 1);
    case "Home":
      return ctrl ? move(0, 0) : move(pos.row, 0);
    case "End":
      return ctrl ? { type: "jumpEnd", to: clampPos({ row: lastRow, col: lastCol }, dims) } : move(pos.row, lastCol);
    case "PageDown":
      return move(Math.max(pos.row, 0) + dims.page, pos.col);
    case "PageUp":
      return move(Math.max(pos.row - dims.page, 0), pos.col);
    case "Enter":
      return { type: "activate", edit: false };
    case "F2":
      return { type: "activate", edit: true };
    case " ":
      return ctrl ? none : { type: "toggleSelect" };
    case "Escape":
      return { type: "cancel" };
    case "c":
    case "C":
      return ctrl && !shift ? { type: "copy" } : none;
    case "ContextMenu":
      return { type: "menu" };
    case "F10":
      return shift ? { type: "menu" } : none;
    default:
      return none;
  }
}

/** step applies an action's move to a position, as the grid does. */
export function step(pos: Pos, dims: Dims, input: KeyInput): Pos {
  const action = keyAction(pos, dims, input);
  return action.type === "move" || action.type === "jumpEnd" ? action.to : pos;
}

/** selectRange is the inclusive range of rows between two, in order. */
export function selectRange(a: number, b: number): number[] {
  const lo = Math.min(a, b);
  const hi = Math.max(a, b);
  return Array.from({ length: hi - lo + 1 }, (_, i) => lo + i);
}

/** announceStep is the size of the steps in which a move is announced to a screen reader. */
export const announceStep = 100;

/** shouldAnnounce is true when moving between two rows crosses a step, or jumps far. */
export function shouldAnnounce(from: number, to: number): boolean {
  return Math.floor(from / announceStep) !== Math.floor(to / announceStep) || Math.abs(to - from) >= announceStep;
}
