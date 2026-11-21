/**
 * A sparse cache of pages of rows.
 *
 * Two modes, picked by the table view:
 * - keyset: rows come with the server's cursor (`after`), so a page can only
 *   be requested once the page before it is known. Wanting a page further
 *   down chains the pages in between, one request at a time.
 * - offset: any page can be requested at any time (`page`, `perPage`).
 *
 * Requests that the viewport moved away from are aborted, at most
 * `maxInFlight` run at once, and at most `maxPages` pages stay in memory.
 * In keyset mode the cursors are kept after a page is evicted, so an evicted
 * page can be fetched again.
 */

export const pageSize = 100;
export const maxCachedPages = 50;
export const maxInFlight = 3;

export type PageMode = "keyset" | "offset";

export interface PageRequest {
  /** Zero-based index of the page. */
  index: number;
  perPage: number;
  /** Keyset mode: the cursor returned with the previous page; absent for the first page. */
  after?: string;
  /** Offset mode: the one-based page number. */
  page?: number;
}

export interface PageResult<Row> {
  rows: Row[];
  /** Keyset mode: the cursor for the page after this one. */
  next?: string;
  hasMore: boolean;
}

export type PageFetcher<Row> = (request: PageRequest, signal: AbortSignal) => Promise<PageResult<Row>>;

export interface PageCacheOptions<Row> {
  fetchPage: PageFetcher<Row>;
  mode: PageMode;
  /** Called after every change a view may need to draw. */
  onChange: () => void;
  size?: number;
  maxPages?: number;
  inFlight?: number;
}

interface Loaded<Row> {
  rows: Row[];
  hasMore: boolean;
}

export class PageCache<Row> {
  private readonly fetchPage: PageFetcher<Row>;
  private readonly mode: PageMode;
  private readonly onChange: () => void;
  private readonly size: number;
  private readonly maxPages: number;
  private readonly cap: number;

  private pages = new Map<number, Loaded<Row>>();
  private cursors: (string | undefined)[] = [];
  private flying = new Map<number, AbortController>();
  private failed = new Map<number, unknown>();
  private key = "";
  private generation = 0;
  private wantedFirst = 0;
  private wantedLast = -1;
  private lastPage = -1;
  private total: number | null = null;
  private disposed = false;

  constructor(options: PageCacheOptions<Row>) {
    this.fetchPage = options.fetchPage;
    this.mode = options.mode;
    this.onChange = options.onChange;
    this.size = options.size ?? pageSize;
    this.maxPages = options.maxPages ?? maxCachedPages;
    this.cap = options.inFlight ?? maxInFlight;
  }

  /** setKey drops everything when the table, filters or sort behind the cache changed. */
  setKey(key: string): void {
    if (key !== this.key) {
      this.key = key;
      this.reset();
    }
  }

  /** reset aborts every request and forgets every page and cursor. */
  reset(): void {
    this.generation++;
    for (const controller of this.flying.values()) {
      controller.abort();
    }
    this.flying.clear();
    this.pages.clear();
    this.cursors = [];
    this.failed.clear();
    this.lastPage = -1;
    this.total = null;
    this.wantedFirst = 0;
    this.wantedLast = -1;
    this.onChange();
  }

  dispose(): void {
    this.disposed = true;
    this.reset();
  }

  /** row returns a loaded row, or undefined while its page is missing. */
  row(index: number): Row | undefined {
    if (index < 0) {
      return undefined;
    }
    const page = this.pages.get(Math.floor(index / this.size));
    return page?.rows[index % this.size];
  }

  get loadedPages(): number {
    return this.pages.size;
  }

  get inFlight(): number {
    return this.flying.size;
  }

  /** The first failure of a page the viewport wants, if any. */
  get error(): unknown {
    for (const [index, err] of this.failed) {
      if (index >= this.wantedFirst && index <= this.wantedLast) {
        return err;
      }
    }
    return null;
  }

  /** The exact number of rows once the last page is known, otherwise null. */
  get exactCount(): number | null {
    return this.total;
  }

  /** The number of rows known to exist: every page up to the last one loaded. */
  get knownRows(): number {
    let n = 0;
    for (const [index, page] of this.pages) {
      n = Math.max(n, index * this.size + page.rows.length);
    }
    return n;
  }

  /** want says which rows (half-open) the viewport shows; missing pages are requested. */
  want(start: number, end: number): void {
    if (this.disposed) {
      return;
    }
    this.wantedFirst = Math.max(Math.floor(start / this.size), 0);
    this.wantedLast = Math.max(Math.floor((end - 1) / this.size), this.wantedFirst);
    this.pump();
  }

  /** retry clears the failures so the next want asks again. */
  retry(): void {
    this.failed.clear();
    this.pump();
  }

  /** refresh refetches the wanted pages in place (live refresh); the old rows stay until the new ones land. */
  refresh(): void {
    if (this.disposed) {
      return;
    }
    this.failed.clear();
    for (let index = this.wantedFirst; index <= this.wantedLast; index++) {
      if (!this.flying.has(index) && this.pages.has(index) && this.requestable(index)) {
        this.start(index);
      }
    }
  }

  private requestable(index: number): boolean {
    if (this.mode === "offset") {
      return true;
    }
    return index === 0 || this.cursors[index - 1] !== undefined;
  }

  /** The pages the viewport needs, plus the chain of pages that leads to them in keyset mode. */
  private required(): number[] {
    const out: number[] = [];
    const top = this.lastPage >= 0 ? Math.min(this.wantedLast, this.lastPage) : this.wantedLast;

    if (this.mode === "offset") {
      for (let i = this.wantedFirst; i <= top; i++) {
        out.push(i);
      }
      return out;
    }

    // The chain starts at the first page whose cursor is not known yet.
    let chain = 0;
    while (chain <= top && this.cursors[chain] !== undefined) {
      chain++;
    }
    for (let i = this.wantedFirst; i <= Math.min(top, chain); i++) {
      out.push(i);
    }
    if (chain <= top && !out.includes(chain)) {
      out.push(chain);
    }
    return out;
  }

  private pump(): void {
    const required = this.required();
    const needed = new Set(required);

    for (const [index, controller] of this.flying) {
      if (!needed.has(index)) {
        controller.abort();
        this.flying.delete(index);
      }
    }

    for (const index of required) {
      if (this.flying.size >= this.cap) {
        break;
      }
      if (this.pages.has(index) || this.flying.has(index) || this.failed.has(index) || !this.requestable(index)) {
        continue;
      }
      this.start(index);
    }
  }

  private start(index: number): void {
    const controller = new AbortController();
    const generation = this.generation;
    this.flying.set(index, controller);

    const request: PageRequest = { index, perPage: this.size };
    if (this.mode === "offset") {
      request.page = index + 1;
    } else if (index > 0) {
      request.after = this.cursors[index - 1];
    }

    this.fetchPage(request, controller.signal).then(
      (result) => this.landed(generation, index, controller, result),
      (err: unknown) => this.broke(generation, index, controller, err),
    );
  }

  private landed(generation: number, index: number, controller: AbortController, result: PageResult<Row>): void {
    if (generation !== this.generation || controller.signal.aborted) {
      return;
    }
    this.flying.delete(index);
    this.failed.delete(index);
    this.pages.set(index, { rows: result.rows, hasMore: result.hasMore });

    if (this.mode === "keyset" && result.hasMore && result.next !== undefined) {
      this.cursors[index] = result.next;
    }
    if (!result.hasMore) {
      this.lastPage = index;
      this.total = index * this.size + result.rows.length;
    }

    this.evict();
    this.pump();
    this.onChange();
  }

  private broke(generation: number, index: number, controller: AbortController, err: unknown): void {
    if (generation !== this.generation || controller.signal.aborted) {
      return;
    }
    this.flying.delete(index);
    this.failed.set(index, err);
    this.onChange();
  }

  /** Drops the pages farthest from the viewport once there are more than maxPages. */
  private evict(): void {
    if (this.pages.size <= this.maxPages) {
      return;
    }
    const center = (this.wantedFirst + Math.max(this.wantedLast, this.wantedFirst)) / 2;
    const byDistance = [...this.pages.keys()]
      .filter((index) => index < this.wantedFirst || index > this.wantedLast)
      .sort((a, b) => Math.abs(b - center) - Math.abs(a - center));

    for (const index of byDistance) {
      if (this.pages.size <= this.maxPages) {
        break;
      }
      this.pages.delete(index);
    }
  }
}
