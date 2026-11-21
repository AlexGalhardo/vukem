import { describe, expect, test } from "bun:test";
import { complete, tablesIn, windowAround, windowLines, wordBefore } from "./complete";
import type { CompletionSource } from "./complete";

const columns: Record<string, string[]> = {
  users: ["id", "name", "email"],
  orders: ["id", "user_id", "total"],
};

const source: CompletionSource = {
  tables: ["users", "orders", "order_items"],
  columns: (table) => columns[table],
};

/** at completes the text before the "|" marker. */
function at(marked: string, explicit = false): { labels: string[]; kinds: string[]; from: number } {
  const cursor = marked.indexOf("|");
  const text = marked.replace("|", "");
  const c = complete(text, cursor, source, explicit);
  return { labels: c.items.map((i) => i.label), kinds: c.items.map((i) => i.kind), from: c.from };
}

describe("wordBefore", () => {
  test("reads the word that ends at the cursor", () => {
    expect(wordBefore("select na", 9)).toEqual({ from: 7, word: "na" });
    expect(wordBefore("select ", 7)).toEqual({ from: 7, word: "" });
    expect(wordBefore("a.b_c1", 6)).toEqual({ from: 2, word: "b_c1" });
  });
});

describe("complete", () => {
  test("offers keywords for a prefix", () => {
    const r = at("sel|");
    expect(r.labels).toContain("SELECT");
    expect(r.from).toBe(0);
  });

  test("offers nothing for an empty word unless asked", () => {
    expect(at("select |").labels).toEqual([]);
    expect(at("select |", true).labels.length > 0).toBe(true);
  });

  test("offers table names after FROM, tables before keywords", () => {
    const r = at("select * from us|");
    expect(r.labels[0]).toBe("users");
    expect(r.kinds[0]).toBe("table");
  });

  test("offers every table after FROM when asked on an empty word", () => {
    const r = at("select * from |", true);
    expect(r.labels.slice(0, 3)).toEqual(["users", "orders", "order_items"]);
  });

  test("offers the columns after table dot", () => {
    expect(at("select users.|").labels).toEqual(["id", "name", "email"]);
    expect(at("select users.na|").labels).toEqual(["name"]);
  });

  test("resolves an alias", () => {
    expect(at("select o.| from orders o").labels).toEqual(["id", "user_id", "total"]);
    expect(at("select u.em| from users as u join orders o on o.user_id = u.id").labels).toEqual(["email"]);
  });

  test("offers the columns of the tables in FROM and JOIN for a bare word", () => {
    const r = at("select tot| from users join orders on 1 = 1");
    expect(r.labels[0]).toBe("total");
    expect(r.kinds[0]).toBe("column");
  });

  test("offers no column of a table that is not loaded", () => {
    expect(at("select order_items.|").labels).toEqual([]);
  });

  test("offers nothing inside a string or a comment", () => {
    expect(at("select 'sel|").labels).toEqual([]);
    expect(at("-- sel|").labels).toEqual([]);
    expect(at("/* sel| */").labels).toEqual([]);
  });

  test("is case-insensitive", () => {
    expect(at("SELECT * FROM US|").labels[0]).toBe("users");
  });

  test("limits the list", () => {
    expect(at("|", true).labels.length <= 12).toBe(true);
  });

  test("a second statement does not see the tables of the first", () => {
    const r = at("select * from orders; select tot|");
    expect(r.kinds).not.toContain("column");
  });
});

describe("tablesIn", () => {
  test("finds tables with aliases", () => {
    const refs = tablesIn("select 1 from users u, orders as o", 0, source.tables);
    expect(refs).toEqual([
      { table: "users", alias: "u" },
      { table: "orders", alias: "o" },
    ]);
  });

  test("ignores names that are not tables", () => {
    expect(tablesIn("select 1 from nope", 0, source.tables)).toEqual([]);
  });
});

describe("a word typed in full", () => {
  test("a lone exact match needs no list", () => {
    expect(at("select|").labels).toEqual([]);
  });

  test("an exact match with other candidates stays, first", () => {
    expect(at("or|").labels.slice(0, 2)).toEqual(["OR", "orders"]);
  });
});

describe("long texts", () => {
  const long = Array.from({ length: 3000 }, (_, i) => `select ${i} from users; -- line ${i}`).join("\n");

  test("reads only the lines around the cursor", () => {
    const cursor = long.indexOf("-- line 1500");
    const w = windowAround(long, cursor);
    expect(w.text.split("\n").length <= 2 * windowLines + 1).toBe(true);
    expect(w.text.length < long.length / 10).toBe(true);
    expect(long.slice(w.offset, w.offset + w.text.length)).toBe(w.text);
    expect(w.offset <= cursor && cursor <= w.offset + w.text.length).toBe(true);
  });

  test("a short text is read whole", () => {
    expect(windowAround("select 1", 3)).toEqual({ text: "select 1", offset: 0 });
  });

  test("completes in the middle of a long text, with offsets back in the whole text", () => {
    const at = long.indexOf("-- line 1500");
    const text = long.slice(0, at) + "sel" + long.slice(at);
    const c = complete(text, at + 3, source);
    expect(c.items.map((i) => i.label)).toContain("SELECT");
    expect(c.from).toBe(at);
  });

  test("the ends of a long text work too", () => {
    const c0 = complete("sel" + long, 3, source);
    expect(c0.from).toBe(0);
    expect(c0.items.length > 0).toBe(true);
    const c1 = complete(long + "\nsel", long.length + 4, source);
    expect(c1.from).toBe(long.length + 1);
    expect(c1.items.length > 0).toBe(true);
  });
});
