import { describe, expect, test } from "bun:test";
import { StatementError } from "../console/runner";
import { classifyCommand, commandName, readCommands, writeCommands } from "./commands";
import { completeCommand, lineAt, redisTokens } from "./language";
import { redisProtocol, replyTable } from "./protocol";

describe("commands", () => {
  test("the name is the first word, in the case the server reads it", () => {
    expect(commandName("get a")).toBe("GET");
    expect(commandName('  "set" a b')).toBe("SET");
    expect(commandName("ınfo")).toBe("ıNFO");
    expect(commandName("")).toBe("");
  });

  test("read, write and unknown commands are told apart", () => {
    expect(classifyCommand("GET a")).toEqual({ name: "GET", write: false, destructive: false, known: true });
    expect(classifyCommand("set a 1")).toEqual({ name: "SET", write: true, destructive: false, known: true });
    expect(classifyCommand("del a").destructive).toBe(true);
    for (const refused of ["EVAL", "FUNCTION", "CONFIG", "FLUSHALL", "FLUSHDB", "KEYS", "SCRIPT", "SHUTDOWN", "DEBUG"]) {
      expect(classifyCommand(`${refused} x`).known).toBe(false);
    }
  });

  test("no command is on both lists and none is a script, config or flush command", () => {
    for (const w of writeCommands) expect(readCommands.includes(w)).toBe(false);
    for (const c of [...readCommands, ...writeCommands]) {
      expect(/^(EVAL|EVALSHA|FUNCTION|FCALL|SCRIPT|CONFIG|FLUSH|KEYS|SHUTDOWN|DEBUG|MODULE|ACL|CLIENT|MONITOR)/.test(c)).toBe(false);
    }
  });
});

describe("completion and tokens", () => {
  test("the first word completes from the read list, and from the write list only in write mode", () => {
    expect(completeCommand("hg", 2, false, false).items.map((i) => i.label)).toEqual(["HGET", "HGETALL"]);
    expect(completeCommand("se", 2, false, false).items.map((i) => i.label)).toEqual([]);
    expect(completeCommand("se", 2, true, false).items.map((i) => i.label)).toEqual(["SET", "SETEX"]);
  });

  test("only the first word of a line completes, and an empty word only on request", () => {
    expect(completeCommand("get ke", 6, false, false).items).toEqual([]);
    expect(completeCommand("", 0, false, false).items).toEqual([]);
    expect(completeCommand("", 0, false, true).items.length).toBeGreaterThan(0);
    expect(completeCommand("get a\nhl", 8, false, false).items.map((i) => i.label)).toEqual(["HLEN"]);
  });

  test("a run sends the line at the caret, or all the text", () => {
    const text = "get a\nhgetall h\nttl a";
    expect(lineAt(text, 8, "cursor")).toBe("hgetall h");
    expect(lineAt(text, 0, "cursor")).toBe("get a");
    expect(lineAt(text, 20, "cursor")).toBe("ttl a");
    expect(lineAt(text, 0, "all")).toBe(text);
  });

  test("the command word, quoted text and numbers get their kinds", () => {
    const kinds = redisTokens('set "a b" 12', false)
      .filter((t) => t.kind !== "space")
      .map((t) => `${t.kind}:${t.text}`);
    expect(kinds).toEqual(["keyword:set", 'string:"a b"', "number:12"]);
  });
});

describe("replies", () => {
  test("a list becomes one value a row, a map becomes field and value", () => {
    expect(replyTable("[a b c]")).toEqual({ columns: ["value"], rows: [["a"], ["b"], ["c"]] });
    expect(replyTable("map[k:v n:1]")).toEqual({ columns: ["field", "value"], rows: [["k", "v"], ["n", "1"]] });
    expect(replyTable("[]").rows).toEqual([]);
  });

  test("anything else is one row a line", () => {
    expect(replyTable("OK")).toEqual({ columns: ["result"], rows: [["OK"]] });
    expect(replyTable("a\nb").rows).toEqual([["a"], ["b"]]);
  });
});

describe("the Redis protocol", () => {
  const p = redisProtocol("my redis");

  test("sends the line to the command route", () => {
    expect(p.request("run", " GET a ", false)).toEqual({ path: "/sources/my%20redis/command", body: { line: "GET a" } });
  });

  test("refuses an empty text and several lines before sending", () => {
    expect(() => p.request("run", "   ", false)).toThrow(StatementError);
    expect(() => p.request("run", "get a\nget b", false)).toThrow(StatementError);
  });

  test("keeps the text of the answer and the table", () => {
    const r = p.result({ result: "[a b]" }, 7);
    expect(r.text).toBe("[a b]");
    expect(r.columns).toEqual(["value"]);
    expect(r.elapsedMs).toBe(7);
  });
});
