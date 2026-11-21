/**
 * The commands the Redis console accepts, copied from the server's allow-lists
 * (rediscmd.go). They only drive completion and the early "needs Write mode"
 * notice: the server decides, and a Go test (TestRedisCommandListsMatchTheInterface)
 * fails when these lists and the server's drift apart.
 */

// readCommands:begin
export const readCommands: readonly string[] = [
  "GET", "MGET", "STRLEN", "GETRANGE", "EXISTS", "TYPE", "TTL", "PTTL", "SCAN", "DBSIZE", "INFO", "PING",
  "HGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HSCAN", "LRANGE", "LLEN", "LINDEX",
  "SMEMBERS", "SCARD", "SISMEMBER", "SSCAN", "ZRANGE", "ZCARD", "ZSCORE", "ZRANK", "ZSCAN",
  "XRANGE", "XREVRANGE", "XLEN", "XINFO", "OBJECT", "MEMORY",
];
// readCommands:end

// writeCommands:begin
export const writeCommands: readonly string[] = [
  "SET", "SETEX", "DEL", "UNLINK", "EXPIRE", "PEXPIRE", "PERSIST", "RENAME", "INCR", "DECR", "INCRBY",
  "HSET", "HDEL", "LPUSH", "RPUSH", "LPOP", "RPOP", "LSET", "LREM", "SADD", "SREM", "ZADD", "ZREM", "XADD", "XDEL",
];
// writeCommands:end

/** Commands that remove data: a run asks for the source's name to be typed. */
export const destructiveCommands: readonly string[] = ["DEL", "UNLINK", "LPOP", "RPOP", "HDEL", "LREM", "SREM", "ZREM", "XDEL", "RENAME"];

/** The first word of a command line, upper-cased the way the server reads it (ASCII only). */
export function commandName(line: string): string {
  const word = /^\s*"?([^\s"]*)/.exec(line)?.[1] ?? "";
  // Only ASCII letters change case: "ınfo" must not become INFO.
  return word.replace(/[a-z]/g, (c) => c.toUpperCase());
}

export interface CommandInfo {
  name: string;
  write: boolean;
  destructive: boolean;
  /** Not on either list: the server will refuse it. */
  known: boolean;
}

export function classifyCommand(line: string): CommandInfo {
  const name = commandName(line);
  const write = writeCommands.includes(name);
  return { name, write, destructive: write && destructiveCommands.includes(name), known: write || readCommands.includes(name) };
}
