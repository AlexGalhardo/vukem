import type { Completion } from "../editor/complete";
import type { EditorLanguage } from "../editor/Editor";
import type { RunScope } from "../editor/Editor";
import type { Token } from "../editor/tokenizer";
import { readCommands, writeCommands } from "./commands";

/** redisTokens colors one line: the command word, quoted arguments and numbers. */
export function redisTokens(line: string, commandSeen: boolean): Token[] {
  const out: Token[] = [];
  let i = 0;
  let seen = commandSeen;
  const push = (kind: Token["kind"], text: string): void => {
    out.push({ kind, text, start: i });
    i += text.length;
  };
  while (i < line.length) {
    const c = line[i] as string;
    if (c === " " || c === "\t") {
      let j = i;
      while (j < line.length && (line[j] === " " || line[j] === "\t")) j++;
      push("space", line.slice(i, j));
    } else if (c === '"') {
      let j = i + 1;
      while (j < line.length && line[j] !== '"') j += line[j] === "\\" ? 2 : 1;
      push("string", line.slice(i, Math.min(j + 1, line.length)));
      seen = true;
    } else {
      let j = i;
      while (j < line.length && line[j] !== " " && line[j] !== "\t" && line[j] !== '"') j++;
      const word = line.slice(i, j);
      push(!seen ? "keyword" : /^-?\d+(\.\d+)?$/.test(word) ? "number" : "identifier", word);
      seen = true;
    }
  }
  return out;
}

/** RedisLineCache colors a text of command lines; every line is its own command, so lines do not depend on each other. */
export class RedisLineCache {
  private lines: { text: string; tokens: Token[] }[] = [];

  update(text: string): readonly (readonly Token[])[] {
    const next = text.split("\n");
    const old = this.lines;
    this.lines = next.map((line, i) => (old[i]?.text === line ? (old[i] as { text: string; tokens: Token[] }) : { text: line, tokens: redisTokens(line, false) }));
    return this.lines.map((l) => l.tokens);
  }
}

const maxSuggestions = 12;

/** completeCommand completes the first word of a line from the commands the console accepts; write commands only when write is on. */
export function completeCommand(text: string, caret: number, write: boolean, explicit: boolean): Completion {
  const lineStart = text.lastIndexOf("\n", caret - 1) + 1;
  const before = text.slice(lineStart, caret);
  const m = /^(\s*)([A-Za-z]*)$/.exec(before);
  if (!m || (m[2] === "" && !explicit)) {
    return { from: caret, items: [] };
  }
  const typed = (m[2] as string).toUpperCase();
  const names = [...readCommands, ...(write ? writeCommands : [])].sort();
  return {
    from: caret - typed.length,
    items: names
      .filter((n) => n.startsWith(typed))
      .slice(0, maxSuggestions)
      .map((label) => ({ label, kind: "keyword" as const })),
  };
}

/** lineAt is the line the caret is on, trimmed: the command a run sends when nothing is selected. */
export function lineAt(text: string, caret: number, scope: RunScope): string {
  if (scope === "all") {
    return text.trim();
  }
  const start = text.lastIndexOf("\n", caret - 1) + 1;
  const end = text.indexOf("\n", caret);
  return text.slice(start, end === -1 ? text.length : end).trim();
}

/** The editor language of the Redis console. Completion offers write commands only in write mode. */
export function redisLanguage(write: boolean): EditorLanguage {
  return {
    newCache: () => new RedisLineCache(),
    complete: (text, caret, explicit) => completeCommand(text, caret, write, explicit),
    statementAt: (text, caret, scope) => lineAt(text, caret, scope),
    comments: false,
  };
}
