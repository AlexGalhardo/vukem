/**
 * The layout of the relationship diagram: a layered drawing, deterministic by
 * construction (no force simulation, no randomness, no clock), so the same
 * schema always gives the same picture.
 *
 * 1. A table sits one layer to the right of the deepest table it references, so
 *    parents come first and a foreign key reads left to right. A foreign key
 *    that would close a cycle is a "back edge": it is left out of the layering
 *    and drawn from the right side of both tables.
 * 2. Inside a layer the order is improved by the barycenter of the neighbours
 *    in the next layer, in a fixed number of sweeps, to reduce crossings. Ties
 *    break on the previous position, then on the name.
 * 3. A layer longer than maxPerColumn is split in several columns, so a schema
 *    of hundreds of tables is not one very tall strip.
 * 4. Edges are orthogonal: out of the side of the table, along a lane in the
 *    gap between columns, into the other table. Lanes are handed out in order,
 *    so two edges do not share a vertical run.
 */

export const nodeWidth = 190;
export const nodeHeight = 46;
export const gapX = 96;
export const gapY = 24;
export const margin = 24;
export const maxPerColumn = 16;

const sweeps = 4;
const laneStep = 7;
const maxLanes = 10;
const sep = "\u0000";

export interface LayoutInput {
  name: string;
  /** The tables its foreign keys point at; repeats and self references are fine. */
  refs: readonly string[];
}

export interface Point {
  x: number;
  y: number;
}

export interface PlacedNode {
  name: string;
  x: number;
  y: number;
  width: number;
  height: number;
  /** The layer (depth by foreign key): 0 holds the tables that reference nothing. */
  layer: number;
  /** The physical column, which is the layer unless a long layer was split. */
  column: number;
}

export interface PlacedEdge {
  /** The table that holds the foreign key. */
  from: string;
  /** The table it points at; the arrow head is here. */
  to: string;
  points: Point[];
}

export interface Layout {
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  width: number;
  height: number;
}

interface Graph {
  names: string[];
  /** child -> unique parents, sorted. Self references and unknown tables are gone. */
  parents: Map<string, string[]>;
  /** Tables with a self reference. */
  selfRef: Set<string>;
}

function compare(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function buildGraph(tables: readonly LayoutInput[]): Graph {
  const names = [...new Set(tables.map((t) => t.name))].sort(compare);
  const known = new Set(names);
  const parents = new Map<string, string[]>();
  const selfRef = new Set<string>();

  for (const name of names) {
    parents.set(name, []);
  }
  for (const t of tables) {
    const list = parents.get(t.name) as string[];
    for (const ref of t.refs) {
      if (ref === t.name) {
        selfRef.add(t.name);
      } else if (known.has(ref) && !list.includes(ref)) {
        list.push(ref);
      }
    }
  }
  for (const list of parents.values()) {
    list.sort(compare);
  }

  return { names, parents, selfRef };
}

/** backEdges finds, walking the tables in name order, the foreign keys that close a cycle. */
function backEdges(g: Graph): Set<string> {
  const back = new Set<string>();
  const state = new Map<string, 1 | 2>(); // 1 on the path, 2 done

  for (const root of g.names) {
    if (state.has(root)) {
      continue;
    }
    // Iterative depth first search over child -> parent edges.
    const stack: { name: string; next: number }[] = [{ name: root, next: 0 }];
    state.set(root, 1);
    while (stack.length > 0) {
      const top = stack[stack.length - 1] as { name: string; next: number };
      const list = g.parents.get(top.name) as string[];
      if (top.next >= list.length) {
        state.set(top.name, 2);
        stack.pop();
        continue;
      }
      const parent = list[top.next++] as string;
      const seen = state.get(parent);
      if (seen === 1) {
        back.add(`${top.name}${sep}${parent}`);
      } else if (seen === undefined) {
        state.set(parent, 1);
        stack.push({ name: parent, next: 0 });
      }
    }
  }

  return back;
}

/** assignLayers gives every table its depth: one more than its deepest parent, ignoring back edges. */
function assignLayers(g: Graph, back: Set<string>): Map<string, number> {
  const layer = new Map<string, number>();
  const children = new Map<string, string[]>();
  const waiting = new Map<string, number>();

  for (const name of g.names) {
    children.set(name, []);
  }
  for (const name of g.names) {
    const ps = (g.parents.get(name) as string[]).filter((p) => !back.has(`${name}${sep}${p}`));
    waiting.set(name, ps.length);
    for (const p of ps) {
      (children.get(p) as string[]).push(name);
    }
  }

  const queue = g.names.filter((n) => waiting.get(n) === 0);
  for (const n of queue) {
    layer.set(n, 0);
  }
  for (let i = 0; i < queue.length; i++) {
    const name = queue[i] as string;
    for (const child of children.get(name) as string[]) {
      layer.set(child, Math.max(layer.get(child) ?? 0, (layer.get(name) as number) + 1));
      const left = (waiting.get(child) as number) - 1;
      waiting.set(child, left);
      if (left === 0) {
        queue.push(child);
      }
    }
  }

  return layer;
}

function neighbours(g: Graph): { up: Map<string, string[]>; down: Map<string, string[]> } {
  const up = new Map<string, string[]>();
  const down = new Map<string, string[]>();
  for (const name of g.names) {
    up.set(name, []);
    down.set(name, []);
  }
  for (const name of g.names) {
    for (const p of g.parents.get(name) as string[]) {
      (up.get(name) as string[]).push(p);
      (down.get(p) as string[]).push(name);
    }
  }

  return { up, down };
}

/** orderLayers sorts the tables inside each layer to cut crossings, by the barycenter of their neighbours. */
function orderLayers(layers: string[][], layerOf: Map<string, number>, g: Graph): void {
  const { up, down } = neighbours(g);
  const position = new Map<string, number>();
  const index = (row: string[]): void => {
    row.forEach((n, i) => position.set(n, i));
  };
  layers.forEach(index);

  const sortLayer = (row: string[], adjacent: Map<string, string[]>, side: number): void => {
    const keyed = row.map((name) => {
      const near = (adjacent.get(name) as string[]).filter((n) => layerOf.get(n) === side);
      const own = position.get(name) as number;
      const bary = near.length === 0 ? own : near.reduce((sum, n) => sum + (position.get(n) as number), 0) / near.length;
      return { name, bary, own };
    });
    keyed.sort((a, b) => a.bary - b.bary || a.own - b.own || compare(a.name, b.name));
    keyed.forEach((k, i) => {
      row[i] = k.name;
    });
    index(row);
  };

  for (let s = 0; s < sweeps; s++) {
    for (let l = 1; l < layers.length; l++) {
      sortLayer(layers[l] as string[], up, l - 1);
    }
    for (let l = layers.length - 2; l >= 0; l--) {
      sortLayer(layers[l] as string[], down, l + 1);
    }
  }
}

interface Port {
  edge: number;
  other: number;
}

interface Raw {
  from: PlacedNode;
  to: PlacedNode;
  /** False for a plain edge (child on the right of the parent); true when both ends use their right side. */
  loop: boolean;
}

/** layoutSchema places the tables and routes the foreign keys between them. */
export function layoutSchema(tables: readonly LayoutInput[]): Layout {
  const g = buildGraph(tables);
  if (g.names.length === 0) {
    return { nodes: [], edges: [], width: 0, height: 0 };
  }

  const back = backEdges(g);
  const layerOf = assignLayers(g, back);
  const depth = Math.max(...layerOf.values());
  const layers: string[][] = Array.from({ length: depth + 1 }, () => []);
  for (const name of g.names) {
    (layers[layerOf.get(name) as number] as string[]).push(name);
  }
  orderLayers(layers, layerOf, g);

  const nodes: PlacedNode[] = [];
  const byName = new Map<string, PlacedNode>();
  let column = 0;
  layers.forEach((row, layer) => {
    for (let start = 0; start < row.length; start += maxPerColumn) {
      row.slice(start, start + maxPerColumn).forEach((name, i) => {
        const node: PlacedNode = {
          name,
          x: margin + column * (nodeWidth + gapX),
          y: margin + i * (nodeHeight + gapY),
          width: nodeWidth,
          height: nodeHeight,
          layer,
          column,
        };
        nodes.push(node);
        byName.set(name, node);
      });
      column++;
    }
  });

  const edges = routeEdges(g, byName, back);
  const width = margin * 2 + column * nodeWidth + (column - 1) * gapX;
  const height = margin + Math.max(...nodes.map((n) => n.y + n.height));

  return { nodes, edges, width, height };
}

function routeEdges(g: Graph, byName: Map<string, PlacedNode>, back: Set<string>): PlacedEdge[] {
  const raws: Raw[] = [];
  for (const name of g.names) {
    const from = byName.get(name) as PlacedNode;
    for (const parent of g.parents.get(name) as string[]) {
      const to = byName.get(parent) as PlacedNode;
      raws.push({ from, to, loop: !(to.column < from.column && !back.has(`${name}${sep}${parent}`)) });
    }
    if (g.selfRef.has(name)) {
      raws.push({ from, to: from, loop: true });
    }
  }

  // Ports: the edges on one side of a table are spread along it, ordered by where the other end is.
  const ports = new Map<string, Port[]>();
  const add = (node: PlacedNode, side: string, edge: number, other: number): void => {
    const key = `${node.name}${sep}${side}`;
    const list = ports.get(key) ?? [];
    list.push({ edge, other });
    ports.set(key, list);
  };
  const sideOf = (r: Raw, end: "from" | "to"): string => (end === "from" && !r.loop ? "left" : "right");
  raws.forEach((r, i) => {
    add(r.from, sideOf(r, "from"), i, r.to.y);
    add(r.to, sideOf(r, "to"), i, r.from.y);
  });
  const portY = (node: PlacedNode, side: string, edge: number): number => {
    const list = (ports.get(`${node.name}${sep}${side}`) as Port[]).slice().sort((a, b) => a.other - b.other || a.edge - b.edge);
    const at = list.findIndex((p) => p.edge === edge);
    return node.y + (node.height * (at + 1)) / (list.length + 1);
  };

  const lanes = new Map<number, number>();
  const lane = (gapIndex: number): number => {
    const used = lanes.get(gapIndex) ?? 0;
    lanes.set(gapIndex, used + 1);
    return (used % maxLanes) * laneStep;
  };

  return raws.map((r, i) => {
    const sy = portY(r.from, sideOf(r, "from"), i);
    // A self reference leaves and enters the same side: the two ends are kept apart so the loop is visible.
    const ty = r.from === r.to ? Math.min(sy + 12, r.to.y + r.to.height - 4) : portY(r.to, sideOf(r, "to"), i);
    const sx = r.loop ? r.from.x + r.from.width : r.from.x;
    const tx = r.to.x + r.to.width;
    // A plain edge runs in the gap right of the parent; a back edge or a loop in the gap right of the right-most end.
    const gapColumn = r.loop ? Math.max(r.from.column, r.to.column) : r.to.column;
    const edge = (r.loop ? Math.max(r.from.x, r.to.x) + nodeWidth : tx) + 12 + lane(gapColumn);
    const points: Point[] = [
      { x: sx, y: sy },
      { x: edge, y: sy },
      { x: edge, y: ty },
      { x: tx, y: ty },
    ];

    return { from: r.from.name, to: r.to.name, points };
  });
}

/** neighboursOf lists the tables joined to name by a foreign key, either way. */
export function neighboursOf(layout: Layout, name: string): Set<string> {
  const out = new Set<string>();
  for (const e of layout.edges) {
    if (e.from === name) out.add(e.to);
    if (e.to === name) out.add(e.from);
  }

  return out;
}
