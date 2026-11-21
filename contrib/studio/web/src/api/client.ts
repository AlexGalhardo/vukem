const base = "/api/v1";

/** A failed API call: the status, the machine code and a message safe to show. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

interface ErrorBody {
  error?: { code?: unknown; message?: unknown };
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
    const { code, message } = body.error ?? {};
    if (typeof code === "string" && typeof message === "string") {
      return new ApiError(res.status, code, message);
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

/** apiPath builds an API path from segments, escaping each one. */
export function apiPath(...segments: string[]): string {
  return "/" + segments.map(encodeURIComponent).join("/");
}
