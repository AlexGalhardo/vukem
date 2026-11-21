import type { ApiCell, Column } from "../api/types";

/** packKey joins key parts the way the API expects a composite key: each part escaped, joined by commas. */
export function packKey(parts: readonly string[]): string {
  return parts.map(encodeURIComponent).join(",");
}

/** unpackKey is the inverse of packKey. */
export function unpackKey(packed: string): string[] {
  return packed.split(",").map((p) => {
    try {
      return decodeURIComponent(p.replace(/\+/g, " "));
    } catch {
      return p;
    }
  });
}

/**
 * keyParts reads the primary key of a row from its cells: null when the table
 * has no key or a part of it is NULL, cut or binary (it could not name the row).
 */
export function keyParts(columns: readonly Column[], primaryKey: readonly string[], cells: readonly ApiCell[]): string[] | null {
  if (primaryKey.length === 0) {
    return null;
  }
  const parts: string[] = [];
  for (const name of primaryKey) {
    const cell = cells[columns.findIndex((c) => c.name === name)];
    if (!cell || cell.n || cell.x || cell.b) {
      return null;
    }
    parts.push(cell.t);
  }
  return parts;
}

/** confirmText is what a person types to confirm deleting the row: the key parts joined by commas. */
export function confirmText(parts: readonly string[]): string {
  return parts.join(",");
}
