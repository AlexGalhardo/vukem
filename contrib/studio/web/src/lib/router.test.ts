import { describe, expect, test } from "bun:test";
import { hrefFor, isPlainLeftClick, matchRoute } from "./router";
import type { Route } from "./router";

describe("matchRoute", () => {
  test("matches the overview with and without the trailing slash", () => {
    expect(matchRoute("/ui")).toEqual({ name: "overview" });
    expect(matchRoute("/ui/")).toEqual({ name: "overview" });
  });

  test("matches a source and a table", () => {
    expect(matchRoute("/ui/s/db")).toEqual({ name: "source", source: "db" });
    expect(matchRoute("/ui/s/db/")).toEqual({ name: "source", source: "db" });
    expect(matchRoute("/ui/s/db/t/users")).toEqual({ name: "table", source: "db", table: "users" });
  });

  test("matches the SQL console of a source", () => {
    expect(matchRoute("/ui/s/db/q")).toEqual({ name: "console", source: "db" });
    expect(matchRoute("/ui/s/db/q/")).toEqual({ name: "console", source: "db" });
    expect(matchRoute("/ui/s/db/q/x")).toEqual({ name: "notfound" });
  });

  test("matches the schema, a collection and a key", () => {
    expect(matchRoute("/ui/s/db/schema")).toEqual({ name: "schema", source: "db" });
    expect(matchRoute("/ui/s/db/schema/x")).toEqual({ name: "notfound" });
    expect(matchRoute("/ui/s/m/c/users")).toEqual({ name: "collection", source: "m", collection: "users" });
    expect(matchRoute("/ui/s/r/k/session%3A1%2Fx")).toEqual({ name: "key", source: "r", key: "session:1/x" });
    expect(matchRoute("/ui/s/r/k")).toEqual({ name: "notfound" });
  });

  test("decodes escaped names", () => {
    expect(matchRoute("/ui/s/my%20db/t/a%2Fb")).toEqual({ name: "table", source: "my db", table: "a/b" });
  });

  test("anything else is not found", () => {
    for (const path of ["/", "/api/v1/bootstrap", "/uix", "/ui/x", "/ui/s", "/ui/s/db/t", "/ui/s/db/t/a/b", "/ui/s/%E0%A4%A"]) {
      expect(matchRoute(path)).toEqual({ name: "notfound" });
    }
  });
});

describe("hrefFor", () => {
  test("is the inverse of matchRoute", () => {
    const routes: Route[] = [
      { name: "overview" },
      { name: "source", source: "db" },
      { name: "table", source: "my db", table: "a/b" },
      { name: "console", source: "my db" },
      { name: "schema", source: "my db" },
      { name: "collection", source: "m", collection: "a b/c" },
      { name: "key", source: "r", key: "user:1/x?y" },
    ];

    for (const route of routes) {
      expect(matchRoute(hrefFor(route))).toEqual(route);
    }
  });
});

describe("isPlainLeftClick", () => {
  const click = { button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, defaultPrevented: false };

  test("accepts a plain left click", () => {
    expect(isPlainLeftClick(click)).toBe(true);
  });

  test("leaves new-tab and modified clicks to the browser", () => {
    expect(isPlainLeftClick({ ...click, button: 1 })).toBe(false);
    expect(isPlainLeftClick({ ...click, ctrlKey: true })).toBe(false);
    expect(isPlainLeftClick({ ...click, metaKey: true })).toBe(false);
    expect(isPlainLeftClick({ ...click, defaultPrevented: true })).toBe(false);
  });
});
