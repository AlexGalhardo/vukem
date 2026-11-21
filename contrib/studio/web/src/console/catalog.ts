import { useEffect, useMemo, useRef, useState } from "react";
import { apiGet, apiPath } from "../api/client";
import type { TableDetail, TablesResponse } from "../api/types";
import { useApi } from "../api/useApi";
import type { CompletionSource } from "../editor/complete";
import { tokenize } from "../editor/tokenizer";

const maxTablesLoaded = 20;

/** The table names of a text: identifiers that name a known table, whatever their case or quotes. */
export function mentionedTables(text: string, known: readonly string[]): string[] {
  const byLower = new Map(known.map((t) => [t.toLowerCase(), t]));
  const found = new Set<string>();
  for (const t of tokenize(text)) {
    if (t.kind === "identifier") {
      const name = byLower.get(t.text.replace(/^["`]|["`]$/g, "").toLowerCase());
      if (name !== undefined) {
        found.add(name);
      }
    }
  }
  return [...found];
}

/**
 * useCatalog gives the editor what it completes from: the table names of the
 * source, and the columns of the tables the text mentions. Both come from the
 * Studio API on this machine, the same calls the table view makes.
 */
export function useCatalog(source: string, text: string): CompletionSource {
  const { data } = useApi<TablesResponse>(apiPath("sources", source, "tables"));
  const [columns, setColumns] = useState<ReadonlyMap<string, readonly string[]>>(new Map());
  const asked = useRef(new Set<string>());
  const tables = useMemo(() => data?.tables.map((t) => t.name) ?? [], [data]);

  useEffect(() => {
    asked.current = new Set();
    setColumns(new Map());
  }, [source]);

  useEffect(() => {
    const wanted = mentionedTables(text, tables).filter((t) => !asked.current.has(t));
    for (const table of wanted.slice(0, Math.max(maxTablesLoaded - asked.current.size, 0))) {
      asked.current.add(table);
      apiGet<TableDetail>(apiPath("sources", source, "tables", table)).then(
        (detail) => setColumns((prev) => new Map(prev).set(table, detail.columns.map((c) => c.name))),
        () => undefined,
      );
    }
  }, [text, tables, source]);

  return useMemo(() => ({ tables, columns: (table) => columns.get(table) }), [tables, columns]);
}
