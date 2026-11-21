import { afterEach, describe, expect, test } from "bun:test";
import { ApiError, apiGet, apiPath } from "./client";

const realFetch = globalThis.fetch;

interface Call {
  url: string;
  init: RequestInit | undefined;
}

function mockFetch(respond: () => Response): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init });
    return respond();
  }) as typeof fetch;
  return calls;
}

afterEach(() => {
  globalThis.fetch = realFetch;
});

describe("apiGet", () => {
  test("reads JSON from /api/v1 with the session cookie and no caching", async () => {
    const calls = mockFetch(() => Response.json({ version: "v1", write: false, refreshSeconds: 2, sources: [] }));

    const got = await apiGet<{ version: string }>("/bootstrap");

    expect(got.version).toBe("v1");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe("/api/v1/bootstrap");
    expect(calls[0]?.init?.credentials).toBe("same-origin");
    expect(calls[0]?.init?.cache).toBe("no-store");
  });

  test("turns an API error body into an ApiError", async () => {
    mockFetch(() => Response.json({ error: { code: "not_found", message: "Not found." } }, { status: 404 }));

    await expect(apiGet("/sources/x/tables")).rejects.toMatchObject({ status: 404, code: "not_found", message: "Not found." });
  });

  test("explains an ended session on 401, whatever the body", async () => {
    mockFetch(() => new Response("<p>token</p>", { status: 401 }));

    await expect(apiGet("/bootstrap")).rejects.toMatchObject({ status: 401, code: "unauthorized" });
  });

  test("falls back to a generic message when the body is not an API error", async () => {
    mockFetch(() => new Response("oops", { status: 502 }));

    await expect(apiGet("/bootstrap")).rejects.toMatchObject({ status: 502, code: "http_error" });
  });

  test("throws an ApiError instance", async () => {
    mockFetch(() => new Response("{}", { status: 500 }));

    await expect(apiGet("/bootstrap")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("apiPath", () => {
  test("escapes every segment", () => {
    expect(apiPath("sources", "my db", "tables", "a/b")).toBe("/sources/my%20db/tables/a%2Fb");
  });
});
