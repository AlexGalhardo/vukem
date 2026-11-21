import { describe, expect, test } from "bun:test";
import { bytesOfEscaped, hexRows, hexText } from "./hex";

const bs = String.fromCharCode(92);

describe("hex view", () => {
  test("reads the server's escapes and the plain bytes between them", () => {
    expect(bytesOfEscaped(`${bs}xff${bs}x00ab`).bytes).toEqual([0xff, 0x00, 0x61, 0x62]);
    expect(bytesOfEscaped(`${bs}xFF`).bytes).toEqual([0xff]);
  });

  test("a trailing ellipsis means the server cut the value", () => {
    const r = bytesOfEscaped("ab…");
    expect(r.cut).toBe(true);
    expect(r.bytes).toEqual([0x61, 0x62]);
    expect(bytesOfEscaped("ab").cut).toBe(false);
  });

  test("a lone backslash or a short escape stays a backslash", () => {
    expect(bytesOfEscaped(`a${bs}xZZ`).bytes).toEqual([0x61, 0x5c, 0x78, 0x5a, 0x5a]);
  });

  test("the dump has 16 bytes a row, the offset, and ASCII for printable bytes", () => {
    const bytes = Array.from({ length: 20 }, (_, i) => (i === 3 ? 0x00 : 0x41 + i));
    const rows = hexRows(bytes);
    expect(rows).toHaveLength(2);
    expect(rows[0]?.offset).toBe("00000000");
    expect(rows[1]?.offset).toBe("00000010");
    expect(rows[0]?.hex.split(" ")).toHaveLength(16);
    expect(rows[0]?.ascii).toBe("ABC.EFGHIJKLMNOP");
    expect(rows[1]?.hex).toBe("51 52 53 54");
  });

  test("the text of the dump lines the columns up", () => {
    const text = hexText(hexRows([0x41, 0x42]));
    expect(text).toBe(`00000000  41 42${" ".repeat(16 * 3 - 1 - 5)}  AB`);
  });

  test("no bytes is no rows", () => {
    expect(hexRows([])).toEqual([]);
  });
});
