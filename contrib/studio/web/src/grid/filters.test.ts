import { describe, expect, test } from "bun:test";
import {
  apiQuery,
  columnKind,
  exportHref,
  nextSort,
  operatorsFor,
  parseFilter,
  searchToView,
  validateFilter,
  viewToSearch,
} from "./filters";
import type { Filter, Operator, ViewState } from "./filters";

const f = (column: string, op: Operator, value = ""): Filter => ({ column, op, value });

describe("parseFilter", () => {
  test("splits column, operator and value", () => {
    expect(parseFilter("name:eq:ada")).toEqual(f("name", "eq", "ada"));
  });

  test("keeps colons and percent signs in the value", () => {
    expect(parseFilter("at:gte:2026-01-02T03:04:05Z")).toEqual(f("at", "gte", "2026-01-02T03:04:05Z"));
    expect(parseFilter("bio:contains:50%_off:now")).toEqual(f("bio", "contains", "50%_off:now"));
    expect(parseFilter("a:eq:")).toEqual(f("a", "eq", ""));
  });

  test("the NULL operators carry no value", () => {
    expect(parseFilter("bio:null")).toEqual(f("bio", "null"));
    expect(parseFilter("bio:notnull:ignored")).toEqual(f("bio", "notnull"));
  });

  test("refuses an unknown operator, a missing one or a missing column", () => {
    for (const raw of ["name:regex:a", "name", "name::a", ":eq:a", "", "name:EQ:a", "name:eq "]) {
      expect(parseFilter(raw)).toBeNull();
    }
  });
});

const states: ViewState[] = [
  { filters: [], sort: null, desc: false },
  { filters: [], sort: "id", desc: false },
  { filters: [], sort: "id", desc: true },
  { filters: [f("name", "eq", "ada")], sort: null, desc: false },
  { filters: [f("name", "contains", "50%_off")], sort: null, desc: false },
  { filters: [f("name", "contains", "100% sure & more")], sort: null, desc: false },
  { filters: [f("at", "gte", "2026-01-02T03:04:05Z")], sort: "at", desc: true },
  { filters: [f("url", "eq", "https://example.test:8080/a?b=c&d=e#f")], sort: null, desc: false },
  { filters: [f("a", "eq", "x:y:z")], sort: null, desc: false },
  { filters: [f("a", "starts", ":leading")], sort: null, desc: false },
  { filters: [f("a", "eq", "trailing:")], sort: null, desc: false },
  { filters: [f("a", "eq", "+plus and space")], sort: null, desc: false },
  { filters: [f("a", "eq", "line\nbreak")], sort: null, desc: false },
  { filters: [f("a", "eq", "ünïcode ✓ 日本語")], sort: "a", desc: false },
  { filters: [f("a", "eq", "'; DROP TABLE users; --")], sort: null, desc: false },
  { filters: [f("a", "eq", "<script>alert(1)</script>")], sort: null, desc: false },
  { filters: [f("a", "eq", "=")], sort: null, desc: false },
  { filters: [f("a", "eq", "&f=b:eq:c")], sort: null, desc: false },
  { filters: [f("bio", "null")], sort: null, desc: false },
  { filters: [f("bio", "notnull")], sort: "bio", desc: false },
  { filters: [f("qty", "gt", "5"), f("qty", "lt", "10")], sort: null, desc: false },
  { filters: [f("qty", "gt", "5"), f("name", "starts", "a"), f("bio", "null")], sort: "name", desc: true },
  { filters: [f("a", "ne", "1"), f("a", "ne", "2"), f("a", "ne", "3")], sort: null, desc: false },
  { filters: [f("with space", "eq", "v")], sort: "with space", desc: false },
  { filters: [f("ünï", "eq", "v")], sort: "ünï", desc: true },
  { filters: [f("a%b", "eq", "v")], sort: null, desc: false },
  { filters: [f("a", "lte", "-1.5e3")], sort: null, desc: false },
  { filters: [f("a", "eq", " padded ")], sort: null, desc: false },
  { filters: [f("a", "eq", "x".repeat(500))], sort: null, desc: false },
  { filters: [f("id", "gte", "1"), f("id", "lte", "99")], sort: "id", desc: true },
];

describe("URL state", () => {
  test("round trips 30 states", () => {
    expect(states).toHaveLength(30);
    for (const view of states) {
      const parsed = searchToView(viewToSearch(view));
      expect(parsed.rejected).toBe(0);
      expect(parsed.view).toEqual(view);
    }
  });

  test("the default view has an empty query string", () => {
    expect(viewToSearch({ filters: [], sort: null, desc: false })).toBe("");
  });

  test("uses f, sort and desc, so a link reads like the API", () => {
    const s = viewToSearch({ filters: [f("name", "eq", "ada")], sort: "id", desc: true });
    expect(s).toBe("?f=name%3Aeq%3Aada&sort=id&desc=1");
  });

  test("an unknown operator is counted as rejected and not applied", () => {
    const parsed = searchToView("?f=name:regex:a&f=id:eq:1");
    expect(parsed.rejected).toBe(1);
    expect(parsed.view.filters).toEqual([f("id", "eq", "1")]);
  });

  test("desc without a sort is ignored", () => {
    expect(searchToView("?desc=1").view).toEqual({ filters: [], sort: null, desc: false });
  });
});

describe("apiQuery", () => {
  test("repeats filter and carries sort, desc and extras", () => {
    const q = apiQuery({ filters: [f("a", "eq", "1"), f("b", "null")], sort: "a", desc: true }, { perPage: "100" });
    const params = new URLSearchParams(q);
    expect(params.getAll("filter")).toEqual(["a:eq:1", "b:null:"]);
    expect(params.get("sort")).toBe("a");
    expect(params.get("desc")).toBe("1");
    expect(params.get("perPage")).toBe("100");
  });
});

describe("operators by type", () => {
  test("column kinds", () => {
    const cases: [string, string][] = [
      ["integer", "number"], ["BIGINT", "number"], ["numeric(10,2)", "number"], ["int unsigned", "number"],
      ["double precision", "number"], ["boolean", "boolean"], ["timestamp with time zone", "date"], ["date", "date"],
      ["jsonb", "json"], ["bytea", "binary"], ["varchar(20)", "text"], ["text", "text"], ["", "text"],
    ];
    for (const [type, kind] of cases) {
      expect(columnKind(type)).toBe(kind);
    }
  });

  test("text offers pattern operators, numbers and dates comparisons", () => {
    expect(operatorsFor("text")).toEqual(["contains", "starts", "eq", "ne", "null", "notnull"]);
    expect(operatorsFor("number")).toContain("gte");
    expect(operatorsFor("date")).toContain("lt");
    expect(operatorsFor("text")).not.toContain("gt");
    expect(operatorsFor("binary")).toEqual(["null", "notnull"]);
  });

  test("validateFilter checks the column, the operator for its type and the value", () => {
    const columns = [
      { name: "id", type: "integer", nullable: false },
      { name: "name", type: "text", nullable: false },
    ];
    expect(validateFilter(f("id", "gt", "1"), columns)).toBeNull();
    expect(validateFilter(f("ghost", "eq", "1"), columns)).toContain("Unknown column");
    expect(validateFilter(f("name", "gt", "a"), columns)).toContain("cannot be filtered");
    expect(validateFilter(f("name", "eq", ""), columns)).toContain("Give a value");
    expect(validateFilter(f("name", "null"), columns)).toBeNull();
  });
});

describe("exportHref", () => {
  test("carries the filters, the sort and the format on the export route", () => {
    const href = exportHref("my db", "a/b", { filters: [f("qty", "gte", "7"), f("bio", "null")], sort: "name", desc: true }, "csv");
    const [path, query] = href.split("?");
    expect(path).toBe("/s/my%20db/t/a%2Fb/export");
    const params = new URLSearchParams(query);
    expect(params.getAll("f")).toEqual(["qty:gte:7", "bio:null:"]);
    expect(params.get("sort")).toBe("name");
    expect(params.get("desc")).toBe("1");
    expect(params.get("format")).toBe("csv");
  });

  test("JSON Lines is the server's json format; the default view has no filters or sort", () => {
    const href = exportHref("db", "users", { filters: [], sort: null, desc: false }, "jsonl");
    expect(href).toBe("/s/db/t/users/export?format=json");
  });
});

describe("nextSort", () => {
  test("cycles ascending, descending, none", () => {
    const start: ViewState = { filters: [f("a", "eq", "1")], sort: null, desc: false };
    const asc = nextSort(start, "name");
    expect(asc).toEqual({ filters: start.filters, sort: "name", desc: false });
    const desc = nextSort(asc, "name");
    expect(desc.desc).toBe(true);
    expect(nextSort(desc, "name")).toEqual(start);
  });

  test("another column starts ascending", () => {
    expect(nextSort({ filters: [], sort: "a", desc: true }, "b")).toEqual({ filters: [], sort: "b", desc: false });
  });
});
