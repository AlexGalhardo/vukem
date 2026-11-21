import { describe, expect, test } from "bun:test";
import type { ApiCell, Column } from "../api/types";
import { confirmText, keyParts, packKey, unpackKey } from "./rowKey";

const columns: Column[] = [
  { name: "a", type: "integer", nullable: false },
  { name: "b", type: "text", nullable: false },
  { name: "v", type: "text", nullable: true },
];

describe("rowKey", () => {
  test("packs and unpacks parts with commas, spaces and percent signs", () => {
    for (const parts of [["1"], ["1", "2"], ["a,b", "c d"], ["50%", "x/y?z=1&w"], ["", "é✓"], ["+plus"]]) {
      expect(unpackKey(packKey(parts))).toEqual(parts);
    }
    expect(packKey(["a,b", "c"])).toBe("a%2Cb,c");
  });

  test("the server's plus for a space is understood", () => {
    expect(unpackKey("a+b,c%2Cd")).toEqual(["a b", "c,d"]);
  });

  test("reads the key of a row in primary key order", () => {
    const cells: ApiCell[] = [{ t: "7", l: true }, { t: "x" }, { t: "", n: true }];
    expect(keyParts(columns, ["a"], cells)).toEqual(["7"]);
    expect(keyParts(columns, ["b", "a"], cells)).toEqual(["x", "7"]);
  });

  test("no key when the table has none or a part is NULL, cut or binary", () => {
    const cells: ApiCell[] = [{ t: "7" }, { t: "x" }, { t: "", n: true }];
    expect(keyParts(columns, [], cells)).toBeNull();
    expect(keyParts(columns, ["v"], cells)).toBeNull();
    expect(keyParts(columns, ["b"], [{ t: "7" }, { t: "xx", x: true }, { t: "" }])).toBeNull();
    expect(keyParts(columns, ["b"], [{ t: "7" }, { t: "<binary 2 bytes>", b: true }, { t: "" }])).toBeNull();
  });

  test("the confirmation text is the plain parts joined by commas", () => {
    expect(confirmText(["1", "a b"])).toBe("1,a b");
  });
});
