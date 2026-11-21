import { apiPath } from "../api/client";
import type { ApiCell, QueryResponse } from "../api/types";
import { StatementError } from "../console/runner";
import type { Protocol } from "../console/runner";

export interface ReplyTable {
  columns: string[];
  rows: string[][];
}

/**
 * replyTable lays the server's text of a reply out as a table. The server
 * prints Go values: a list as [a b c] and a map as map[k:v k2:v2], so a value
 * that holds a space is split in two here. The text view keeps it whole.
 */
export function replyTable(text: string): ReplyTable {
  const map = /^map\[(.*)\]$/s.exec(text);
  if (map) {
    const body = map[1] as string;
    return {
      columns: ["field", "value"],
      rows: body === "" ? [] : body.split(" ").map((pair) => {
        const at = pair.indexOf(":");
        return at < 0 ? [pair, ""] : [pair.slice(0, at), pair.slice(at + 1)];
      }),
    };
  }
  const list = /^\[(.*)\]$/s.exec(text);
  if (list) {
    const body = list[1] as string;
    return { columns: ["value"], rows: body === "" ? [] : body.split(" ").map((v) => [v]) };
  }
  return { columns: ["result"], rows: text.split("\n").map((line) => [line]) };
}

const cutAt = 300;

function cell(t: string): ApiCell {
  return t.length > cutAt ? { t: t.slice(0, cutAt), x: true } : { t };
}

/** The Redis console: one command line to /command, its text answer shown as text or as a table. */
export function redisProtocol(source: string): Protocol {
  return {
    request: (_kind, statement) => {
      const line = statement.trim();
      if (line === "") {
        throw new StatementError("Type a command to run.");
      }
      if (line.includes("\n")) {
        throw new StatementError("Run one command at a time: put the caret on its line.");
      }
      return { path: apiPath("sources", source, "command"), body: { line } };
    },
    result: (raw, elapsedMs): QueryResponse => {
      const text = (raw as { result: string }).result;
      const table = replyTable(text);
      return {
        columns: table.columns,
        rows: table.rows.map((r) => r.map(cell)),
        truncated: false,
        elapsedMs,
        kind: "COMMAND",
        text,
      };
    },
  };
}
