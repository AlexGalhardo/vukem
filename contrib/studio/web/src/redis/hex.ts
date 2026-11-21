/**
 * The hex view of a binary Redis string. The server cannot send raw bytes in
 * JSON, so it shows a value that is not valid UTF-8 as text where every byte
 * that is not printable ASCII is written as a backslash, an x and two hex
 * digits. This reads that text back into bytes and lays them out as a hex dump.
 */

export const rowBytes = 16;

const escape = /^\\x([0-9a-fA-F]{2})/;

/** bytesOfEscaped reads the server's escapes back into bytes. A trailing "…" (the server cut the value) is dropped. */
export function bytesOfEscaped(text: string): { bytes: number[]; cut: boolean } {
  const cut = text.endsWith("…");
  const body = cut ? text.slice(0, -1) : text;
  const bytes: number[] = [];
  for (let i = 0; i < body.length; ) {
    const m = escape.exec(body.slice(i, i + 4));
    if (m) {
      bytes.push(parseInt(m[1] as string, 16));
      i += 4;
    } else {
      bytes.push(body.charCodeAt(i) & 0xff);
      i++;
    }
  }
  return { bytes, cut };
}

export interface HexRow {
  offset: string;
  hex: string;
  ascii: string;
}

/** hexRows is the dump: 16 bytes a row, their hex, and the printable ASCII of them. */
export function hexRows(bytes: readonly number[]): HexRow[] {
  const rows: HexRow[] = [];
  for (let i = 0; i < bytes.length; i += rowBytes) {
    const chunk = bytes.slice(i, i + rowBytes);
    rows.push({
      offset: i.toString(16).padStart(8, "0"),
      hex: chunk.map((b) => b.toString(16).padStart(2, "0")).join(" "),
      ascii: chunk.map((b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : ".")).join(""),
    });
  }
  return rows;
}

/** hexText is the dump as one block of text, for copying. */
export function hexText(rows: readonly HexRow[]): string {
  return rows.map((r) => `${r.offset}  ${r.hex.padEnd(rowBytes * 3 - 1)}  ${r.ascii}`).join("\n");
}
