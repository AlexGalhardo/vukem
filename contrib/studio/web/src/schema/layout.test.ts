import { describe, expect, test } from "bun:test";
import { gapX, layoutSchema, maxPerColumn, neighboursOf, nodeHeight, nodeWidth } from "./layout";
import type { Layout, LayoutInput } from "./layout";

const shop: LayoutInput[] = [
  { name: "orders", refs: ["customers"] },
  { name: "items", refs: ["orders", "products"] },
  { name: "customers", refs: [] },
  { name: "products", refs: ["vendors"] },
  { name: "vendors", refs: [] },
  { name: "notes", refs: [] },
];

function overlaps(layout: Layout): [string, string][] {
  const found: [string, string][] = [];
  const n = layout.nodes;
  for (let i = 0; i < n.length; i++) {
    for (let j = i + 1; j < n.length; j++) {
      const a = n[i]!;
      const b = n[j]!;
      if (a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height) {
        found.push([a.name, b.name]);
      }
    }
  }
  return found;
}

function big(count: number): LayoutInput[] {
  const tables: LayoutInput[] = [];
  for (let i = 0; i < count; i++) {
    const refs: string[] = [];
    if (i > 0) refs.push(`t${String(Math.floor(i / 3)).padStart(3, "0")}`);
    if (i > 10 && i % 4 === 0) refs.push(`t${String((i * 7) % i).padStart(3, "0")}`);
    tables.push({ name: `t${String(i).padStart(3, "0")}`, refs });
  }
  return tables;
}

describe("layoutSchema", () => {
  test("puts a table one layer right of the deepest table it references", () => {
    const l = layoutSchema(shop);
    const layer = (n: string): number => l.nodes.find((x) => x.name === n)!.layer;
    expect(layer("customers")).toBe(0);
    expect(layer("vendors")).toBe(0);
    expect(layer("orders")).toBe(1);
    expect(layer("products")).toBe(1);
    expect(layer("items")).toBe(2);
    expect(layer("notes")).toBe(0);
  });

  test("is the same picture on 100 runs, whatever the order of the input", () => {
    const first = JSON.stringify(layoutSchema(shop));
    for (let i = 0; i < 100; i++) {
      const shuffled = [...shop].sort((a, b) => ((a.name.charCodeAt(0) * (i + 3)) % 7) - ((b.name.charCodeAt(0) * (i + 3)) % 7));
      expect(JSON.stringify(layoutSchema(shuffled))).toBe(first);
    }
  });

  test("never overlaps two tables", () => {
    expect(overlaps(layoutSchema(shop))).toEqual([]);
    expect(overlaps(layoutSchema(big(300)))).toEqual([]);
  });

  test("keeps every table inside the reported size", () => {
    const l = layoutSchema(big(120));
    for (const n of l.nodes) {
      expect(n.x + n.width).toBeLessThanOrEqual(l.width);
      expect(n.y + n.height).toBeLessThanOrEqual(l.height);
    }
  });

  test("handles a cycle, a self reference and a reference to an unknown table", () => {
    const l = layoutSchema([
      { name: "a", refs: ["b"] },
      { name: "b", refs: ["c"] },
      { name: "c", refs: ["a", "c", "ghost"] },
    ]);
    expect(l.nodes.map((n) => n.name).sort()).toEqual(["a", "b", "c"]);
    // Three foreign keys between tables (one closes the cycle) and one self reference; "ghost" is dropped.
    expect(l.edges.length).toBe(4);
    expect(overlaps(l)).toEqual([]);
    for (const e of l.edges) {
      expect(e.points.length).toBeGreaterThanOrEqual(4);
      expect(e.points.every((p) => Number.isFinite(p.x) && Number.isFinite(p.y))).toBe(true);
    }
  });

  test("draws one edge per distinct foreign key target, with orthogonal segments only", () => {
    const l = layoutSchema([
      { name: "a", refs: [] },
      { name: "b", refs: ["a", "a"] },
    ]);
    expect(l.edges).toHaveLength(1);
    const pts = l.edges[0]!.points;
    for (let i = 1; i < pts.length; i++) {
      expect(pts[i]!.x === pts[i - 1]!.x || pts[i]!.y === pts[i - 1]!.y).toBe(true);
    }
    const arrow = pts[pts.length - 1]!;
    const parent = l.nodes.find((n) => n.name === "a")!;
    expect(arrow.x).toBe(parent.x + parent.width);
  });

  test("splits a long layer in columns", () => {
    const tables: LayoutInput[] = Array.from({ length: maxPerColumn * 2 + 1 }, (_, i) => ({ name: `x${String(i).padStart(2, "0")}`, refs: [] }));
    const l = layoutSchema(tables);
    expect(new Set(l.nodes.map((n) => n.column)).size).toBe(3);
    expect(Math.max(...l.nodes.map((n) => n.y))).toBeLessThan(maxPerColumn * (nodeHeight + 30));
    expect(l.nodes.every((n) => n.layer === 0)).toBe(true);
    expect(l.width).toBeGreaterThanOrEqual(3 * nodeWidth + 2 * gapX);
  });

  test("an empty schema is an empty picture", () => {
    expect(layoutSchema([])).toEqual({ nodes: [], edges: [], width: 0, height: 0 });
  });

  test("lays out 300 tables in under 500 ms", () => {
    const tables = big(300);
    layoutSchema(tables);
    const start = performance.now();
    const l = layoutSchema(tables);
    const took = performance.now() - start;
    expect(l.nodes).toHaveLength(300);
    expect(took).toBeLessThan(500);
  });

  test("neighboursOf reads a foreign key both ways", () => {
    const l = layoutSchema(shop);
    expect([...neighboursOf(l, "orders")].sort()).toEqual(["customers", "items"]);
    expect([...neighboursOf(l, "notes")]).toEqual([]);
  });
});
