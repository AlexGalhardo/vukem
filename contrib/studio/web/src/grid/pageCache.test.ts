import { describe, expect, test } from "bun:test";
import { PageCache } from "./pageCache";
import type { PageMode, PageRequest, PageResult } from "./pageCache";

interface Pending {
  request: PageRequest;
  signal: AbortSignal;
  resolve: (result: PageResult<number>) => void;
  reject: (err: unknown) => void;
}

/** A fetcher whose responses the test settles by hand. */
function harness(mode: PageMode, options: { size?: number; maxPages?: number; total?: number } = {}) {
  const size = options.size ?? 10;
  const total = options.total ?? 1_000;
  const pending: Pending[] = [];
  const started: PageRequest[] = [];
  let changes = 0;

  const cache = new PageCache<number>({
    mode,
    size,
    maxPages: options.maxPages,
    onChange: () => changes++,
    fetchPage: (request, signal) =>
      new Promise<PageResult<number>>((resolve, reject) => {
        started.push(request);
        pending.push({ request, signal, resolve, reject });
      }),
  });

  /** The page the server would answer for a request. */
  const answer = (request: PageRequest): PageResult<number> => {
    const first = mode === "offset" ? ((request.page ?? 1) - 1) * size : request.index * size;
    const rows: number[] = [];
    for (let i = first; i < Math.min(first + size, total); i++) {
      rows.push(i);
    }
    const hasMore = first + size < total;
    return { rows, hasMore, next: mode === "keyset" && hasMore ? `c${first + size}` : undefined };
  };

  const settle = async (index: number): Promise<void> => {
    const p = pending.find((x) => x.request.index === index && !x.signal.aborted);
    if (!p) {
      throw new Error(`no live request for page ${index}`);
    }
    pending.splice(pending.indexOf(p), 1);
    p.resolve(answer(p.request));
    await Promise.resolve();
    await Promise.resolve();
  };

  const live = (): number[] => pending.filter((p) => !p.signal.aborted).map((p) => p.request.index);

  return { cache, pending, started, settle, live, changes: () => changes };
}

describe("offset mode", () => {
  test("requests every wanted page, at most three at a time", async () => {
    const h = harness("offset");
    h.cache.want(0, 60);
    expect(h.live()).toEqual([0, 1, 2]);
    expect(h.cache.inFlight).toBe(3);

    await h.settle(0);
    expect(h.live().sort()).toEqual([1, 2, 3]);
    expect(h.cache.row(5)).toBe(5);
    expect(h.cache.row(15)).toBeUndefined();
  });

  test("asks for page numbers, not cursors", () => {
    const h = harness("offset");
    h.cache.want(30, 40);
    expect(h.started).toEqual([{ index: 3, perPage: 10, page: 4 }]);
  });

  test("a fast scroll cancels the requests for pages that left the viewport", () => {
    const h = harness("offset");
    h.cache.want(0, 30);
    const first = [...h.pending];
    expect(h.live()).toEqual([0, 1, 2]);

    h.cache.want(500, 520);
    expect(first.every((p) => p.signal.aborted)).toBe(true);
    expect(h.live()).toEqual([50, 51]);
  });

  test("a response that arrives after its request was cancelled is ignored", async () => {
    const h = harness("offset");
    h.cache.want(0, 10);
    const stale = h.pending[0];
    h.cache.want(500, 510);
    stale?.resolve({ rows: [99], hasMore: true });
    await Promise.resolve();
    await Promise.resolve();
    expect(h.cache.row(0)).toBeUndefined();
  });

  test("keeps at most maxPages pages and evicts the farthest from the viewport", async () => {
    const h = harness("offset", { maxPages: 5, total: 10_000 });
    for (let page = 0; page < 12; page++) {
      h.cache.want(page * 10, page * 10 + 10);
      await h.settle(page);
    }
    expect(h.cache.loadedPages).toBe(5);
    expect(h.cache.row(115)).toBe(115);
    expect(h.cache.row(5)).toBeUndefined();
  });

  test("an evicted page is fetched again when wanted", async () => {
    const h = harness("offset", { maxPages: 2, total: 10_000 });
    for (const page of [0, 1, 2, 3]) {
      h.cache.want(page * 10, page * 10 + 10);
      await h.settle(page);
    }
    expect(h.cache.row(5)).toBeUndefined();
    h.cache.want(0, 10);
    expect(h.live()).toEqual([0]);
  });

  test("the last page tells the exact count", async () => {
    const h = harness("offset", { total: 25 });
    h.cache.want(20, 30);
    await h.settle(2);
    expect(h.cache.exactCount).toBe(25);
  });
});

describe("keyset mode", () => {
  test("loads the first page without a cursor and the next with the cursor of the previous", async () => {
    const h = harness("keyset");
    h.cache.want(0, 20);
    expect(h.started).toEqual([{ index: 0, perPage: 10 }]);

    await h.settle(0);
    expect(h.started[1]).toEqual({ index: 1, perPage: 10, after: "c10" });
  });

  test("wanting a far page chains the pages in between, one request at a time", async () => {
    const h = harness("keyset");
    h.cache.want(40, 50);
    expect(h.live()).toEqual([0]);

    for (const page of [0, 1, 2, 3]) {
      await h.settle(page);
    }
    expect(h.live()).toEqual([4]);
    expect(h.cache.inFlight).toBe(1);
    expect(h.started.map((r) => r.after)).toEqual([undefined, "c10", "c20", "c30", "c40"]);
  });

  test("the chain stops when the viewport moves back", async () => {
    const h = harness("keyset");
    h.cache.want(90, 100);
    await h.settle(0);
    expect(h.live()).toEqual([1]);
    h.cache.want(0, 10);
    expect(h.live()).toEqual([]);
    expect(h.cache.row(3)).toBe(3);
  });

  test("an evicted page is refetched with the kept cursor", async () => {
    const h = harness("keyset", { maxPages: 2, total: 10_000 });
    for (const page of [0, 1, 2, 3]) {
      h.cache.want(page * 10, page * 10 + 10);
      await h.settle(page);
    }
    expect(h.cache.row(15)).toBeUndefined();
    h.cache.want(10, 20);
    expect(h.started.at(-1)).toEqual({ index: 1, perPage: 10, after: "c10" });
  });

  test("the last page ends the chain", async () => {
    const h = harness("keyset", { total: 25 });
    h.cache.want(0, 100);
    await h.settle(0);
    await h.settle(1);
    await h.settle(2);
    expect(h.live()).toEqual([]);
    expect(h.cache.exactCount).toBe(25);
    expect(h.cache.knownRows).toBe(25);
  });
});

describe("setRow", () => {
  test("replaces a loaded row and tells the view", async () => {
    const h = harness("offset");
    h.cache.want(0, 10);
    await h.settle(0);
    const before = h.changes();
    expect(h.cache.setRow(3, 333)).toBe(true);
    expect(h.cache.row(3)).toBe(333);
    expect(h.cache.row(4)).toBe(4);
    expect(h.changes()).toBe(before + 1);
  });

  test("does nothing for a row that is not loaded", () => {
    const h = harness("offset");
    expect(h.cache.setRow(3, 1)).toBe(false);
    expect(h.cache.setRow(-1, 1)).toBe(false);
  });
});

describe("keys and refresh", () => {
  test("a new key drops pages, aborts requests and forgets the cursors", async () => {
    const h = harness("keyset");
    h.cache.setKey("users|name:eq:a");
    h.cache.want(0, 20);
    await h.settle(0);
    const inFlight = [...h.pending];

    h.cache.setKey("users|name:eq:a");
    expect(h.cache.row(0)).toBe(0);

    h.cache.setKey("users|name:eq:b");
    expect(h.cache.row(0)).toBeUndefined();
    expect(h.cache.loadedPages).toBe(0);
    expect(inFlight.every((p) => p.signal.aborted)).toBe(true);

    h.cache.want(0, 10);
    expect(h.started.at(-1)).toEqual({ index: 0, perPage: 10 });
  });

  test("a response from before the reset does not fill the new cache", async () => {
    const h = harness("offset");
    h.cache.setKey("a");
    h.cache.want(0, 10);
    const old = h.pending[0];
    h.cache.setKey("b");
    old?.resolve({ rows: [7], hasMore: true });
    await Promise.resolve();
    await Promise.resolve();
    expect(h.cache.row(0)).toBeUndefined();
  });

  test("refresh refetches the wanted pages and keeps the old rows until they land", async () => {
    const h = harness("offset");
    h.cache.want(0, 20);
    await h.settle(0);
    await h.settle(1);
    const before = h.started.length;

    h.cache.refresh();
    expect(h.started.length).toBe(before + 2);
    expect(h.cache.row(3)).toBe(3);
  });

  test("a failed page is not retried until retry is called", async () => {
    const h = harness("offset");
    h.cache.want(0, 10);
    h.pending[0]?.reject(new Error("boom"));
    await Promise.resolve();
    await Promise.resolve();

    expect(h.cache.error instanceof Error).toBe(true);
    h.cache.want(0, 10);
    expect(h.started.length).toBe(1);

    h.cache.retry();
    expect(h.started.length).toBe(2);
    expect(h.cache.error).toBeNull();
  });

  test("a disposed cache requests nothing", () => {
    const h = harness("offset");
    h.cache.dispose();
    h.cache.want(0, 10);
    expect(h.started).toEqual([]);
  });
});
