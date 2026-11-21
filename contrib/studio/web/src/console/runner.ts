import { ApiError, apiPath, apiSend } from "../api/client";
import type { QueryResponse } from "../api/types";

export type RunKind = "run" | "explain";
export type ErrorKind = "input" | "timeout" | "database" | "refused" | "network" | "other";

export interface RunError {
  kind: ErrorKind;
  /** The kind in words, for the person reading it. */
  label: string;
  /** The server's generic message: it never carries what the database said. */
  message: string;
}

export type RunState =
  | { status: "idle" }
  | { status: "running"; kind: RunKind; statement: string; startedAt: number }
  | { status: "done"; id: number; kind: RunKind; statement: string; result: QueryResponse; roundTripMs: number }
  | { status: "error"; kind: RunKind; statement: string; error: RunError }
  | { status: "cancelled"; kind: RunKind; statement: string };

export const idle: RunState = { status: "idle" };

const labels: Record<ErrorKind, string> = {
  input: "Input error",
  timeout: "Timeout",
  database: "Database error",
  refused: "Refused",
  network: "Connection error",
  other: "Error",
};

/** describeError maps a failed call to the kind a person can act on. */
export function describeError(err: unknown): RunError {
  if (!(err instanceof ApiError)) {
    return { kind: "network", label: labels.network, message: "Studio could not be reached." };
  }
  let kind: ErrorKind = "other";
  switch (err.code) {
    case "invalid_input":
      kind = "input";
      break;
    case "timeout":
      kind = "timeout";
      break;
    case "query_failed":
      kind = "database";
      break;
    case "write_disabled":
      kind = "refused";
      break;
    case "network":
      kind = "network";
      break;
  }
  return { kind, label: labels[kind], message: err.message };
}

/** Sends one statement to the API; injected so the store can be tested without a server. */
export type Sender = (path: string, body: unknown, signal: AbortSignal) => Promise<unknown>;

const defaultSend: Sender = (path, body, signal) => apiSend<unknown>("POST", path, body, signal);

/**
 * What differs between consoles: the route and body a statement goes to, and how
 * the answer becomes the QueryResponse that the result view and the history
 * know. SQL posts to query or explain; the MongoDB and Redis consoles have
 * their own (see mongo/protocol.ts and redis/protocol.ts). A request that
 * throws shows its message as an input error and nothing is sent.
 */
export interface Protocol {
  request: (kind: RunKind, statement: string, write: boolean) => { path: string; body: unknown };
  result: (raw: unknown, elapsedMs: number) => QueryResponse;
}

/** inputError is what a Protocol throws for a statement it refuses before sending. */
export class StatementError extends Error {}

export function sqlProtocol(source: string): Protocol {
  return {
    request: (kind, statement, write) => ({
      path: apiPath("sources", source, kind === "run" ? "query" : "explain"),
      body: kind === "run" ? { sql: statement, write } : { sql: statement },
    }),
    result: (raw) => raw as QueryResponse,
  };
}

/**
 * RunnerStore keeps the run state of each tab of one source. A run belongs to
 * a tab: switching tabs neither stops it nor loses its result. Results stay in
 * memory only; nothing here is written to storage.
 */
export class RunnerStore {
  private readonly states = new Map<string, RunState>();
  private readonly controllers = new Map<string, AbortController>();
  private readonly listeners = new Set<() => void>();
  private seq = 0;

  constructor(
    source: string,
    private readonly send: Sender = defaultSend,
    private readonly now: () => number = () => performance.now(),
    private readonly protocol: Protocol = sqlProtocol(source),
  ) {}

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  get(tab: string): RunState {
    return this.states.get(tab) ?? idle;
  }

  private set(tab: string, state: RunState): void {
    this.states.set(tab, state);
    this.listeners.forEach((l) => l());
  }

  /** start runs or explains a statement. A run already going on the tab is aborted first. */
  async start(tab: string, kind: RunKind, statement: string, write: boolean): Promise<void> {
    this.controllers.get(tab)?.abort();
    const controller = new AbortController();
    this.controllers.set(tab, controller);
    const startedAt = this.now();
    const id = ++this.seq;
    this.set(tab, { status: "running", kind, statement, startedAt });

    try {
      const { path, body } = this.protocol.request(kind, statement, write);
      const raw = await this.send(path, body, controller.signal);
      if (this.controllers.get(tab) === controller) {
        const roundTripMs = Math.round(this.now() - startedAt);
        this.set(tab, { status: "done", id, kind, statement, result: this.protocol.result(raw, roundTripMs), roundTripMs });
      }
    } catch (err) {
      if (err instanceof StatementError) {
        if (this.controllers.get(tab) === controller) {
          this.set(tab, { status: "error", kind, statement, error: { kind: "input", label: labels.input, message: err.message } });
        }
        return;
      }
      if (this.controllers.get(tab) !== controller) {
        return;
      }
      if (controller.signal.aborted || (err instanceof ApiError && err.code === "canceled")) {
        this.set(tab, { status: "cancelled", kind, statement });
      } else {
        this.set(tab, { status: "error", kind, statement, error: describeError(err) });
      }
    } finally {
      if (this.controllers.get(tab) === controller) {
        this.controllers.delete(tab);
      }
    }
  }

  /** cancel aborts the request of a tab; the server stops the statement when the connection closes. */
  cancel(tab: string): void {
    const state = this.get(tab);
    const controller = this.controllers.get(tab);
    if (state.status === "running" && controller) {
      this.controllers.delete(tab);
      controller.abort();
      this.set(tab, { status: "cancelled", kind: state.kind, statement: state.statement });
    }
  }

  /** forget drops a tab's state and aborts its run: the tab was closed. */
  forget(tab: string): void {
    this.controllers.get(tab)?.abort();
    this.controllers.delete(tab);
    if (this.states.delete(tab)) {
      this.listeners.forEach((l) => l());
    }
  }
}
