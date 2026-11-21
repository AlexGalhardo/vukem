export type SourceKind = "sql" | "mongo" | "redis";

export interface Source {
  name: string;
  kind: SourceKind;
  label: string;
}

export interface Bootstrap {
  version: string;
  write: boolean;
  refreshSeconds: number;
  /** Names this Studio run for the browser storage of the interface. */
  sessionKey: string;
  sources: Source[];
}

export interface TableSummary {
  name: string;
  rowEstimate: number;
  primaryKey: string[];
}

export interface TablesResponse {
  tables: TableSummary[];
}

export interface Column {
  name: string;
  type: string;
  nullable: boolean;
  default?: string;
}

export interface ForeignKey {
  columns: string[];
  refTable: string;
  refColumns: string[];
}

export interface TableDetail {
  name: string;
  columns: Column[];
  primaryKey: string[];
  foreignKeys: ForeignKey[];
}

export interface CollectionSummary {
  name: string;
  estimate: number;
}

export interface CollectionsResponse {
  collections: CollectionSummary[];
}

export interface KeySummary {
  key: string;
  type: string;
  ttlSeconds: number;
}

export interface KeysResponse {
  keys: KeySummary[];
  cursor?: string;
}

/** One value of a row: t is the text, n is NULL, x is cut for display, b is binary, l is a JSON number or boolean. */
export interface ApiCell {
  t: string;
  n?: boolean;
  x?: boolean;
  b?: boolean;
  l?: boolean;
}

export interface RowsResponse {
  columns: Column[];
  rows: ApiCell[][];
  next?: string;
  hasMore: boolean;
}

export interface RowResponse {
  columns: Column[];
  cells: ApiCell[];
}

export interface CountResponse {
  count?: number;
  timedOut?: boolean;
}

export interface Reference {
  table: string;
  columns: string[];
  refColumns: string[];
}

export interface ReferencesResponse {
  references: Reference[];
}

/** The answer of a console statement: one result set, or Affected for a write. */
export interface QueryResponse {
  columns: string[];
  rows: ApiCell[][];
  /** True when the rows were cut at the console row or size limit. */
  truncated: boolean;
  elapsedMs: number;
  /** The verb of the statement in upper case. */
  kind: string;
  affected?: number;
}
