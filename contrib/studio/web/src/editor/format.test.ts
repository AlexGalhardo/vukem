import { describe, expect, test } from "bun:test";
import { format } from "./format";
import { tokenize } from "./tokenizer";

/** The tokens a formatter must keep: everything but spaces, keywords compared without case. */
function significant(sql: string): string[] {
  return tokenize(sql)
    .filter((t) => t.kind !== "space")
    .map((t) => (t.kind === "keyword" ? t.text.toLowerCase() : t.text));
}

const fixtures: string[] = [
  "select 1",
  "select a,b from t where x=1 and y=2 order by a desc limit 10",
  "SELECT * FROM users u JOIN orders o ON o.user_id=u.id AND o.total>10 WHERE u.name LIKE 'a%'",
  "select count(*) from t group by a having count(*)>1",
  "select a from t left outer join u on t.id=u.id inner join v using (id)",
  "select * from (select id from t where id in (1,2,3)) x",
  "with a as (select 1), b as (select 2) select * from a, b",
  "insert into t (a,b) values (1,'x'),(2,'y')",
  "update t set a=1,b='two' where id=3",
  "delete from t where id between 1 and 5 and x is not null",
  "select 'it''s -- not a comment', \"Quoted Name\", `bt` from t",
  "select a -- trailing\nfrom t",
  "-- leading comment\nselect 1",
  "select /* inline */ a from t",
  "select a /* multi\nline */ from t",
  "select $$ keep   this $$ from t",
  "select 'multi\nline   string'",
  "select a::int, -1, a - -1, a=-1, - b from t",
  "select case when a>1 then 'x' else 'y' end from t",
  "select * from t union all select * from u",
  "select 1;select 2;",
  "explain select * from t",
  "explain query plan select * from t",
  "select t.* from t",
  "select a->>'k', b||c from t",
  "select sum(x) over (partition by g order by h) from t",
  "select * from t where a in (select b from u where c=1) and d=2",
  "SELECT   a   ,   b\n\n\nFROM\n   t",
  "select * from t where a = :name and b = $1 and c = ?",
  "select left(a, 2), cast(b as int), replace(c, 'x', 'y') from t",
  "select * from t order by 1 offset 5",
  "select 0xFF, 1.5e3, .5 from t",
  "create table t (id integer primary key, name text not null)",
  "select * from t where (a=1 or b=2) and c=3",
  "delete from t",
  "pragma table_info(t)",
  "",
  "   ",
  "select",
  "((select 1))",
];

describe("format", () => {
  test("has at least 30 fixtures", () => {
    expect(fixtures.filter((f) => f.trim() !== "").length >= 30).toBe(true);
  });

  for (const sql of fixtures) {
    test(`is idempotent and keeps the tokens: ${JSON.stringify(sql)}`, () => {
      const once = format(sql);
      expect(format(once)).toBe(once);
      expect(significant(once)).toEqual(significant(sql));
    });
  }

  test("puts one clause on each line and upper-cases keywords", () => {
    expect(format("select a,b from t where x=1 and y=2 order by a desc limit 10")).toBe(
      ["SELECT a, b", "FROM t", "WHERE x = 1", "  AND y = 2", "ORDER BY a DESC", "LIMIT 10"].join("\n"),
    );
  });

  test("breaks before joins and keeps the ON condition", () => {
    expect(format("select * from a left join b on a.id=b.id inner join c on c.id=a.id")).toBe(
      ["SELECT *", "FROM a", "LEFT JOIN b ON a.id = b.id", "INNER JOIN c ON c.id = a.id"].join("\n"),
    );
  });

  test("indents a subquery", () => {
    expect(format("select * from t where id in (select id from u)")).toBe(
      ["SELECT *", "FROM t", "WHERE id IN (", "  SELECT id", "  FROM u", ")"].join("\n"),
    );
  });

  test("does not break BETWEEN ... AND", () => {
    expect(format("select 1 from t where a between 1 and 2 and b=3")).toBe(
      ["SELECT 1", "FROM t", "WHERE a BETWEEN 1 AND 2", "  AND b = 3"].join("\n"),
    );
  });

  test("keeps strings, quoted names and comments as they are", () => {
    const out = format("select 'a   b', \"C  d\" -- keep   this\nfrom t");
    expect(out).toContain("'a   b'");
    expect(out).toContain('"C  d"');
    expect(out).toContain("-- keep   this");
  });

  test("writes function calls without a space and keywords with one", () => {
    expect(format("select count(*), x in (1,2) from t")).toBe(["SELECT count(*), x IN (1, 2)", "FROM t"].join("\n"));
  });

  test("never joins two minus signs into a comment", () => {
    expect(format("select a - -1")).toBe("SELECT a - -1");
  });

  test("a line comment ends its line", () => {
    expect(format("select a -- c\n, b")).toBe(["SELECT a -- c", "  , b"].join("\n"));
  });
});
