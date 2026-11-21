import type { ApiCell } from "./types";

const base = "/api/v1";

/** A failed API call: the status, the machine code and a message safe to show. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** On a 409 conflict: the values the guarded columns hold now. */
  readonly current?: Record<string, ApiCell>;

  constructor(status: number, code: string, message: string, current?: Record<string, ApiCell>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.current = current;
  }
}

interface ErrorBody {
  error?: { code?: unknown; message?: unknown; current?: unknown };
}

function isCells(v: unknown): v is Record<string, ApiCell> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

async function toApiError(res: Response): Promise<ApiError> {
  if (res.status === 401) {
    return new ApiError(
      401,
      "unauthorized",
      "The session ended. Open the address that Studio printed in the terminal.",
    );
  }

  try {
    const body = (await res.json()) as ErrorBody;
    const { code, message, current } = body.error ?? {};
    if (typeof code === "string" && typeof message === "string") {
      return new ApiError(res.status, code, message, isCells(current) ? current : undefined);
    }
  } catch {
    // The body was not JSON: fall through to the generic message.
  }

  return new ApiError(res.status, "http_error", `The request failed with status ${res.status}.`);
}

/** apiGet reads one JSON document from the Studio API. */
export async function apiGet<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(base + path, {
    signal,
    headers: { Accept: "application/json" },
    credentials: "same-origin",
    cache: "no-store",
  });

  if (!res.ok) {
    throw await toApiError(res);
  }

  return (await res.json()) as T;
}

/** apiSend sends a JSON body with a write method (POST, PATCH, DELETE) and reads the JSON answer. */
export async function apiSend<T>(
  method: "POST" | "PATCH" | "DELETE",
  path: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const res = await fetch(base + path, {
    method,
    signal,
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
  });

  if (!res.ok) {
    throw await toApiError(res);
  }

  return (await res.json()) as T;
}

/** apiPath builds an API path from segments, escaping each one. */
export function apiPath(...segments: string[]): string {
  return "/" + segments.map(encodeURIComponent).join("/");
}
