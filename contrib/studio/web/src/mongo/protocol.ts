import { apiPath } from "../api/client";
import type { QueryResponse } from "../api/types";
import { StatementError } from "../console/runner";
import type { Protocol } from "../console/runner";
import { columnsOf, inferColumns, parseDoc, rowOf } from "./docs";
import { checkConsole } from "./json";

interface DocsResponse {
  docs: { id: string; json: string }[];
  hasMore: boolean;
}

/** resultOf turns the documents of a find or an aggregate into columns and rows: the fields of the first documents. */
export function resultOf(raw: DocsResponse, kind: string, elapsedMs: number): QueryResponse {
  const docs = raw.docs.map((d) => parseDoc(d.json));
  const names = inferColumns(docs);
  return {
    columns: columnsOf(names).map((c) => c.name),
    rows: docs.map((d) => rowOf(names, d)),
    truncated: raw.hasMore,
    elapsedMs,
    kind,
  };
}

/**
 * The MongoDB console: the statement is one JSON object, {"collection": ...,
 * "filter": {...}} for a find or {"collection": ..., "pipeline": [...]} for an
 * aggregate. It is checked before it is sent; the server checks it again.
 */
export function mongoProtocol(source: string): Protocol {
  return {
    request: (_kind, statement, write) => {
      const check = checkConsole(statement, write);
      if (!check.ok) {
        throw new StatementError(check.message);
      }
      const r = check.request;
      return r.kind === "find"
        ? { path: apiPath("sources", source, "find"), body: { collection: r.collection, filter: r.filter } }
        : { path: apiPath("sources", source, "aggregate"), body: { collection: r.collection, pipeline: r.pipeline } };
    },
    result: (raw, elapsedMs) => resultOf(raw as DocsResponse, "DOCUMENTS", elapsedMs),
  };
}
