import { describe, expect, test } from "bun:test";
import { classify } from "./classify";

describe("classify", () => {
  test("reads are not writes", () => {
    for (const sql of [
      "select 1",
      "  -- c\n select * from t",
      "(select 1)",
      "with a as (select 1) select * from a",
      "explain select 1",
      "show tables",
      "values (1)",
      "pragma table_info(t)",
      "select 'update', \"delete\" from t",
    ]) {
      expect(classify(sql).write).toBe(false);
    }
  });

  test("writes are", () => {
    for (const sql of [
      "insert into t values (1)",
      "update t set a = 1 where id = 1",
      "delete from t where id = 1",
      "create table t (id int)",
      "alter table t add c int",
      "select * into t2 from t",
      "pragma user_version = 3",
      "with x as (select 1) delete from t",
      "call p()",
    ]) {
      expect(classify(sql).write).toBe(true);
    }
  });

  test("the kind is the verb, or the write verb a WITH carries", () => {
    expect(classify("update t set a = 1").kind).toBe("UPDATE");
    expect(classify("with x as (select 1) delete from t where a = 1").kind).toBe("DELETE");
    expect(classify("with x as (select 1) select * from x").kind).toBe("WITH");
    expect(classify("").kind).toBe("");
    expect(classify("-- only a comment").write).toBe(false);
  });

  test("DROP, TRUNCATE and an UPDATE or DELETE without WHERE are destructive", () => {
    expect(classify("drop table t").destructive).toBe(true);
    expect(classify("truncate t").destructive).toBe(true);
    expect(classify("delete from t").destructive).toBe(true);
    expect(classify("update t set a = 1").destructive).toBe(true);
    expect(classify("delete from t where id = 1").destructive).toBe(false);
    expect(classify("update t set a = 1 where id = 1").destructive).toBe(false);
    expect(classify("insert into t values (1)").destructive).toBe(false);
    expect(classify("update t set a = 1").warning).toContain("no WHERE");
  });

  test("a WHERE in a string or a comment does not count", () => {
    expect(classify("delete from t -- where id = 1").destructive).toBe(true);
    expect(classify("delete from t where_x = 'where'").destructive).toBe(true);
  });
});
