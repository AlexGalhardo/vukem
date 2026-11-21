import { describe, expect, test } from "bun:test";
import { ApiError } from "../api/client";
import type { ApiCell } from "../api/types";
import { StreamStore } from "./stream";
import type { Chunk, FetchChunk } from "./stream";

const row = (t: string): ApiCell[] => [{ t }];
const keyOf = (r: ApiCell[]): string => r[0]?.t ?? "";

function store(fetchChunk: FetchChunk, maxEmptySteps?: number): StreamStore {
  return new StreamStore({ fetchChunk, keyOf, maxEmptySteps, onChange: () => undefined });
}

function script(chunks: Chunk[], seen: (string | null)[] = []): FetchChunk {
  let i = 0;
  return async (cursor) => {
    seen.push(cursor);
    return chunks[Math.min(i++, chunks.length - 1)] as Chunk;
  };
}

describe("StreamStore", () => {
  test("loads in order, passing the cursor each step, until there is no more", async () => {
    const seen: (string | null)[] = [];
    const s = store(
      script(
        [
          { rows: [row("a"), row("b")], next: "7", hasMore: true },
          { rows: [row("c")], next: null, hasMore: false },
        ],
        seen,
      ),
    );
    await s.loadMore();
    expect(s.rows.map(keyOf)).toEqual(["a", "b"]);
    expect(s.hasMore).toBe(true);
    await s.loadMore();
    expect(s.rows.map(keyOf)).toEqual(["a", "b", "c"]);
    expect(s.hasMore).toBe(false);
    expect(seen).toEqual([null, "7"]);
    await s.loadMore();
    expect(seen).toHaveLength(2);
  });

  test("a key that SCAN returned twice is shown once, and counted", async () => {
    const s = store(
      script([
        { rows: [row("k1"), row("k2")], next: "5", hasMore: true },
        { rows: [row("k2"), row("k3")], next: null, hasMore: false },
      ]),
    );
    await s.loadMore();
    await s.loadMore();
    expect(s.rows.map(keyOf)).toEqual(["k1", "k2", "k3"]);
    expect(s.duplicates).toBe(1);
  });

  test("an empty step that says there is more is followed by another, up to a limit", async () => {
    const calls: number[] = [];
    let n = 0;
    const fetchChunk: FetchChunk = async () => {
      calls.push(n++);
      return n < 4 ? { rows: [], next: String(n), hasMore: true } : { rows: [row("x")], next: null, hasMore: false };
    };
    const s = store(fetchChunk);
    await s.loadMore();
    expect(calls).toHaveLength(4);
    expect(s.rows.map(keyOf)).toEqual(["x"]);

    const endless = store(async () => ({ rows: [], next: "1", hasMore: true }), 3);
    await endless.loadMore();
    expect(endless.steps).toBe(3);
    expect(endless.hasMore).toBe(true);
    expect(endless.loading).toBe(false);
  });

  test("a step that brings only repeats counts as empty", async () => {
    let n = 0;
    const s = store(async () => (n++ === 0 ? { rows: [row("a")], next: "1", hasMore: true } : n === 2 ? { rows: [row("a")], next: "2", hasMore: true } : { rows: [row("b")], next: null, hasMore: false }));
    await s.loadMore();
    await s.loadMore();
    expect(s.rows.map(keyOf)).toEqual(["a", "b"]);
  });

  test("a failure keeps the rows, shows the error and retry asks again", async () => {
    let fail = true;
    const s = store(async (cursor) => {
      if (cursor !== null && fail) {
        throw new ApiError(500, "query_failed", "The database could not run the request.");
      }
      return cursor === null ? { rows: [row("a")], next: "1", hasMore: true } : { rows: [row("b")], next: null, hasMore: false };
    });
    await s.loadMore();
    await s.loadMore();
    expect(s.error?.code).toBe("query_failed");
    expect(s.rows.map(keyOf)).toEqual(["a"]);
    fail = false;
    s.retry();
    await new Promise((r) => setTimeout(r, 0));
    expect(s.error).toBeNull();
    expect(s.rows.map(keyOf)).toEqual(["a", "b"]);
  });

  test("reset drops the rows and ignores the step in flight", async () => {
    let release: (c: Chunk) => void = () => undefined;
    const s = store(
      () =>
        new Promise<Chunk>((resolve) => {
          release = resolve;
        }),
    );
    const pending = s.loadMore();
    s.reset();
    release({ rows: [row("late")], next: null, hasMore: false });
    await pending;
    expect(s.rows).toEqual([]);
    expect(s.hasMore).toBe(true);
    expect(s.loading).toBe(false);
  });

  test("only one step runs at a time", async () => {
    let calls = 0;
    const s = store(async () => {
      calls++;
      await new Promise((r) => setTimeout(r, 5));
      return { rows: [row(String(calls))], next: "n", hasMore: true };
    });
    await Promise.all([s.loadMore(), s.loadMore(), s.loadMore()]);
    expect(calls).toBe(1);
  });
});
