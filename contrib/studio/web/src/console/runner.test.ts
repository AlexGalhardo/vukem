import { describe, expect, test } from "bun:test";
import { ApiError } from "../api/client";
import type { QueryResponse } from "../api/types";
import { describeError, RunnerStore } from "./runner";
import type { Sender } from "./runner";

const response: QueryResponse = { columns: ["n"], rows: [[{ t: "1" }]], truncated: false, elapsedMs: 3, kind: "SELECT" };

interface Deferred {
  promise: Promise<QueryResponse>;
  resolve: (r: QueryResponse) => void;
  reject: (e: unknown) => void;
}

function deferred(): Deferred {
  let resolve!: (r: QueryResponse) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<QueryResponse>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("RunnerStore", () => {
  test("runs a statement and keeps the result for the tab", async () => {
    const calls: { path: string; body: unknown }[] = [];
    const send: Sender = async (path, body) => {
      calls.push({ path, body });
      return response;
    };
    const store = new RunnerStore("my db", send, () => 100);
    await store.start("t1", "run", "select 1", false);

    expect(calls).toEqual([{ path: "/sources/my%20db/query", body: { sql: "select 1", write: false } }]);
    expect(store.get("t1")).toEqual({ status: "done", id: 1, kind: "run", statement: "select 1", result: response, roundTripMs: 0 });
    expect(store.get("t2").status).toBe("idle");
  });

  test("explain posts to the explain route and never asks for write", async () => {
    const calls: { path: string; body: unknown }[] = [];
    const store = new RunnerStore("db", async (path, body) => {
      calls.push({ path, body });
      return response;
    });
    await store.start("t", "explain", "select 1", true);
    expect(calls).toEqual([{ path: "/sources/db/explain", body: { sql: "select 1" } }]);
  });

  test("a failure is an error of the right kind, with the server's message", async () => {
    const store = new RunnerStore("db", async () => {
      throw new ApiError(422, "invalid_input", "read-only: only SELECT runs");
    });
    await store.start("t", "run", "delete from t", false);
    const state = store.get("t");
    expect(state.status === "error" && state.error).toEqual({
      kind: "input",
      label: "Input error",
      message: "read-only: only SELECT runs",
    });
  });

  test("cancel aborts the request and shows cancelled, not an error", async () => {
    const d = deferred();
    let signal: AbortSignal | undefined;
    const store = new RunnerStore("db", (_p, _b, s) => {
      signal = s;
      s.addEventListener("abort", () => d.reject(new DOMException("aborted", "AbortError")));
      return d.promise;
    });
    const running = store.start("t", "run", "select 1", false);
    expect(store.get("t").status).toBe("running");
    store.cancel("t");
    await running;
    expect(signal?.aborted).toBe(true);
    expect(store.get("t").status).toBe("cancelled");
  });

  test("a second run on a tab aborts the first and only the second lands", async () => {
    const first = deferred();
    const second = deferred();
    const queue = [first, second];
    const store = new RunnerStore("db", (_p, _b, s) => {
      const d = queue.shift() as Deferred;
      s.addEventListener("abort", () => d.reject(new DOMException("aborted", "AbortError")));
      return d.promise;
    });
    const a = store.start("t", "run", "select 1", false);
    const b = store.start("t", "run", "select 2", false);
    second.resolve(response);
    await Promise.all([a, b]);
    const state = store.get("t");
    expect(state.status === "done" && state.statement).toBe("select 2");
  });

  test("a result that arrives after the tab was closed is dropped", async () => {
    const d = deferred();
    const store = new RunnerStore("db", () => d.promise);
    const running = store.start("t", "run", "select 1", false);
    store.forget("t");
    d.resolve(response);
    await running;
    expect(store.get("t").status).toBe("idle");
  });

  test("notifies subscribers on every change", async () => {
    const store = new RunnerStore("db", async () => response);
    let n = 0;
    const off = store.subscribe(() => n++);
    await store.start("t", "run", "select 1", false);
    off();
    await store.start("t", "run", "select 1", false);
    expect(n).toBe(2);
  });
});

describe("describeError", () => {
  test("names the kinds", () => {
    expect(describeError(new ApiError(504, "timeout", "too long")).kind).toBe("timeout");
    expect(describeError(new ApiError(500, "query_failed", "x")).label).toBe("Database error");
    expect(describeError(new ApiError(403, "write_disabled", "x")).kind).toBe("refused");
    expect(describeError(new TypeError("fetch failed")).kind).toBe("network");
  });
});
