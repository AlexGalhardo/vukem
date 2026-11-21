import { describe, expect, test } from "bun:test";
import type { ApiCell, Column } from "../api/types";
import { foreignKeyLink, referenceLink } from "./links";

const columns: Column[] = [
  { name: "id", type: "integer", nullable: false },
  { name: "user_id", type: "integer", nullable: true },
];
const tables = [
  { name: "users", rowEstimate: 2, primaryKey: ["id"] },
  { name: "teams", rowEstimate: 2, primaryKey: ["code"] },
];

describe("foreignKeyLink", () => {
  const fk = { columns: ["user_id"], refTable: "users", refColumns: ["id"] };

  test("filters the other table and opens its row when the key is its primary key", () => {
    const cells: ApiCell[] = [{ t: "5" }, { t: "1" }];
    const link = foreignKeyLink(fk, columns, cells, tables);
    expect(link?.table).toBe("users");
    expect(link?.search).toBe("?f=id%3Aeq%3A1&open=1");
  });

  test("does not open a row when the target columns are not its primary key", () => {
    const link = foreignKeyLink({ ...fk, refTable: "teams", refColumns: ["id"] }, columns, [{ t: "5" }, { t: "1" }], tables);
    expect(link?.search).toBe("?f=id%3Aeq%3A1");
  });

  test("a NULL key points nowhere", () => {
    expect(foreignKeyLink(fk, columns, [{ t: "5" }, { t: "", n: true }], tables)).toBeNull();
  });
});

describe("referenceLink", () => {
  test("lists the referencing rows by the value of the referenced columns", () => {
    const link = referenceLink({ table: "posts", columns: ["user_id"], refColumns: ["id"] }, columns, [{ t: "7" }, { t: "" }]);
    expect(link).toEqual({ label: "posts (user_id)", table: "posts", search: "?f=user_id%3Aeq%3A7" });
  });
});
