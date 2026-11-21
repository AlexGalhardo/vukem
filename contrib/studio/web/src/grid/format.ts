import type { ApiCell, Column } from "../api/types";
import { columnKind } from "./filters";
import type { ColumnKind } from "./filters";

/** How a cell is drawn: the text to show and the kind of token it is. */
export interface CellView {
  tone: "null" | "number" | "boolean" | "date" | "json" | "binary" | "text";
  text: string;
}

const safeNumber = /^-?\d+(\.\d+)?([eE][+-]?\d+)?$/;

/** jsonCompact writes valid JSON on one line; text that is not JSON is returned as it is. */
export function jsonCompact(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text) as unknown);
  } catch {
    return text;
  }
}

/** jsonPretty indents valid JSON, or returns null when the text is not JSON. */
export function jsonPretty(text: string): string | null {
  try {
    return JSON.stringify(JSON.parse(text) as unknown, null, 2);
  } catch {
    return null;
  }
}

/** cellView decides how a cell looks, by its value and the kind of its column. */
export function cellView(cell: ApiCell, kind: ColumnKind): CellView {
  if (cell.n) {
    return { tone: "null", text: "NULL" };
  }
  if (cell.b) {
    return { tone: "binary", text: cell.t };
  }
  switch (kind) {
    case "number":
      return { tone: "number", text: cell.t };
    case "boolean":
      return { tone: "boolean", text: cell.t === "1" ? "true" : cell.t === "0" ? "false" : cell.t };
    case "date":
      return { tone: "date", text: cell.t };
    case "json":
      return { tone: "json", text: cell.x ? cell.t : jsonCompact(cell.t) };
    default:
      return { tone: cell.l && safeNumber.test(cell.t) ? "number" : "text", text: cell.t };
  }
}

/** plainText is what a copy puts on the clipboard for one cell: NULL is empty. */
export function plainText(cell: ApiCell): string {
  return cell.n ? "" : cell.t;
}

function tsvField(s: string): string {
  return s.replace(/[\t\r\n]+/g, " ");
}

/** toTSV writes rows as tab-separated text, with a header line. */
export function toTSV(columns: readonly Column[], rows: readonly (readonly ApiCell[])[]): string {
  const lines = [columns.map((c) => tsvField(c.name)).join("\t")];
  for (const row of rows) {
    lines.push(row.map((cell) => tsvField(plainText(cell))).join("\t"));
  }
  return lines.join("\n");
}

function csvField(s: string): string {
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

/** toCSV writes rows as RFC 4180 CSV, with a header line; NULL is an empty field. */
export function toCSV(columns: readonly Column[], rows: readonly (readonly ApiCell[])[]): string {
  const lines = [columns.map((c) => csvField(c.name)).join(",")];
  for (const row of rows) {
    lines.push(row.map((cell) => csvField(plainText(cell))).join(","));
  }
  return lines.join("\r\n");
}

/** jsonValue is a cell as a JSON value: NULL, a number, a boolean, parsed JSON or text. */
export function jsonValue(cell: ApiCell, kind: ColumnKind): unknown {
  if (cell.n) {
    return null;
  }
  if (cell.l) {
    // SQLite and MySQL hold booleans as 0 and 1.
    if (kind === "boolean" && (cell.t === "0" || cell.t === "1")) {
      return cell.t === "1";
    }
    if (cell.t === "true" || cell.t === "false") {
      return cell.t === "true";
    }
    const n = Number(cell.t);
    // A number that doubles cannot hold is kept as text, so no digit is lost.
    return safeNumber.test(cell.t) && String(n) === cell.t ? n : cell.t;
  }
  if (kind === "json" && !cell.x) {
    try {
      return JSON.parse(cell.t) as unknown;
    } catch {
      return cell.t;
    }
  }
  return cell.t;
}

/** toJSON writes rows as an indented array of objects keyed by column name. */
export function toJSON(columns: readonly Column[], rows: readonly (readonly ApiCell[])[]): string {
  const kinds = columns.map((c) => columnKind(c.type));
  const out = rows.map((row) => {
    const obj: Record<string, unknown> = {};
    columns.forEach((c, i) => {
      const cell = row[i];
      obj[c.name] = cell ? jsonValue(cell, kinds[i] ?? "text") : null;
    });
    return obj;
  });
  return JSON.stringify(out, null, 2);
}

const kindWidth: Record<ColumnKind, number> = {
  number: 120,
  boolean: 100,
  date: 200,
  json: 240,
  binary: 170,
  text: 220,
};

/** columnWidth is a fixed width in pixels: by kind, never narrower than the header needs. */
export function columnWidth(column: Column): number {
  const header = column.name.length * 8 + 64;
  return Math.max(kindWidth[columnKind(column.type)], Math.min(header, 360));
}

/** formatInt groups digits for the footer. */
export function formatInt(n: number): string {
  return Math.round(n).toLocaleString("en-US");
}
