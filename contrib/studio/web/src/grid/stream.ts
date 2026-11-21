import { ApiError } from "../api/client";
import type { ApiCell } from "../api/types";

/**
 * StreamStore keeps the rows of a list that arrives in steps: a MongoDB
 * collection read by cursor or by page, a Redis SCAN, the elements of a key.
 * Each step gives some rows and where the next one starts. Rows are appended
 * in order; a row whose key was already seen is dropped (SCAN may return a key
 * twice), and a step that brings nothing new but says there is more is
 * followed by another one, up to a limit, so one click does not end on an
 * empty page.
 */

export interface Chunk {
  rows: ApiCell[][];
  /** Where the next step starts; null when there is none. */
  next: string | null;
  hasMore: boolean;
}

export type FetchChunk = (cursor: string | null, step: number, signal: AbortSignal) => Promise<Chunk>;

export interface StreamOptions {
  fetchChunk: FetchChunk;
  /** The identity of a row, to drop repeats. Without it every row is kept. */
  keyOf?: (row: ApiCell[]) => string | undefined;
  /** How many empty steps in a row one loadMore may chain. */
  maxEmptySteps?: number;
  onChange: () => void;
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, "network", "Studio could not be reached.");
}

export class StreamStore {
  rows: ApiCell[][] = [];
  hasMore = true;
  loading = false;
  error: ApiError | null = null;
  /** Rows dropped because their key was already listed. */
  duplicates = 0;
  /** Steps taken since the last reset. */
  steps = 0;
  /** Bumps on every change, for views that compare. */
  version = 0;

  private next: string | null = null;
  private seen = new Set<string>();
  private controller: AbortController | null = null;
  private generation = 0;
  private disposed = false;

  constructor(private readonly options: StreamOptions) {}

  /** reset forgets everything and aborts the step in flight; call loadMore to start again. */
  reset(): void {
    this.generation++;
    this.controller?.abort();
    this.controller = null;
    this.rows = [];
    this.hasMore = true;
    this.loading = false;
    this.error = null;
    this.duplicates = 0;
    this.steps = 0;
    this.next = null;
    this.seen = new Set();
    this.changed();
  }

  dispose(): void {
    this.disposed = true;
    this.reset();
  }

  private changed(): void {
    this.version++;
    if (!this.disposed) {
      this.options.onChange();
    }
  }

  /** loadMore takes one step (or several, when steps come back empty). It does nothing while a step runs or after the last one. */
  async loadMore(): Promise<void> {
    if (this.loading || !this.hasMore || this.disposed) {
      return;
    }
    const generation = this.generation;
    const limit = this.options.maxEmptySteps ?? 20;
    this.loading = true;
    this.error = null;
    this.changed();

    try {
      for (let empty = 0; ; ) {
        const controller = new AbortController();
        this.controller = controller;
        const chunk = await this.options.fetchChunk(this.next, this.steps, controller.signal);
        if (generation !== this.generation) {
          return;
        }
        this.steps++;
        const before = this.rows.length;
        for (const row of chunk.rows) {
          const key = this.options.keyOf?.(row);
          if (key !== undefined) {
            if (this.seen.has(key)) {
              this.duplicates++;
              continue;
            }
            this.seen.add(key);
          }
          this.rows.push(row);
        }
        this.next = chunk.next;
        this.hasMore = chunk.hasMore && chunk.next !== null;
        this.changed();
        empty = this.rows.length === before ? empty + 1 : 0;
        if (!this.hasMore || this.rows.length > before || empty >= limit) {
          break;
        }
      }
    } catch (err) {
      if (generation === this.generation) {
        this.error = asApiError(err);
      }
    } finally {
      if (generation === this.generation) {
        this.loading = false;
        this.controller = null;
        this.changed();
      }
    }
  }

  /** retry asks again after a failure. */
  retry(): void {
    this.error = null;
    void this.loadMore();
  }
}
