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
