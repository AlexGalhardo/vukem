package studio

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// redisReadCommands may always run from the console.
var redisReadCommands = []string{ //nolint:gochecknoglobals // read-only allow-list
	"GET", "MGET", "STRLEN", "GETRANGE", "EXISTS", "TYPE", "TTL", "PTTL", "SCAN", "DBSIZE", "INFO", "PING",
	"HGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HSCAN", "LRANGE", "LLEN", "LINDEX",
	"SMEMBERS", "SCARD", "SISMEMBER", "SSCAN", "ZRANGE", "ZCARD", "ZSCORE", "ZRANK", "ZSCAN",
	"XRANGE", "XREVRANGE", "XLEN", "XINFO", "OBJECT", "MEMORY",
}

// redisWriteCommands run only with --write.
var redisWriteCommands = []string{ //nolint:gochecknoglobals // read-only allow-list
	"SET", "SETEX", "DEL", "UNLINK", "EXPIRE", "PEXPIRE", "PERSIST", "RENAME", "INCR", "DECR", "INCRBY",
	"HSET", "HDEL", "LPUSH", "RPUSH", "LPOP", "RPOP", "LSET", "LREM", "SADD", "SREM", "ZADD", "ZREM", "XADD", "XDEL",
}

// parseRedisLine splits a command line into arguments. Double quotes group
// words and may escape a quote or a backslash with a backslash.
func parseRedisLine(line string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inQuote bool
		started bool
	)

	rs := []rune(line)

	for i := 0; i < len(rs); i++ {
		r := rs[i]

		switch {
		case inQuote && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
			i++

			cur.WriteRune(rs[i])
		case r == '"':
			inQuote = !inQuote
			started = true
		case !inQuote && (r == ' ' || r == '\t'):
			if started {
				args = append(args, cur.String())
				cur.Reset()

				started = false
			}
		default:
			cur.WriteRune(r)

			started = true
		}
	}

	if inQuote {
		return nil, errors.New("studio: unterminated quote")
	}

	if started {
		args = append(args, cur.String())
	}

	if len(args) == 0 {
		return nil, errors.New("studio: empty command")
	}

	return args, nil
}

// checkRedisArgs decides whether the console may run args. It is an
// allow-list: a command that is not on it, new or unknown, is refused.
func checkRedisArgs(args []string, write bool) error {
	cmd := asciiUpper(args[0])

	switch {
	case slices.Contains(redisReadCommands, cmd):
		return checkRedisSubcommand(cmd, args)
	case slices.Contains(redisWriteCommands, cmd):
		if !write {
			return fmt.Errorf("studio: command %q writes data and needs --write", cmd)
		}

		return nil
	default:
		return fmt.Errorf("command %q is not allowed in vukem-studio", args[0])
	}
}

// checkRedisSubcommand keeps MEMORY and OBJECT to their read subcommands.
func checkRedisSubcommand(cmd string, args []string) error {
	var allowed []string

	switch cmd {
	case "MEMORY":
		allowed = []string{"USAGE"}
	case "OBJECT":
		allowed = []string{"ENCODING", "IDLETIME", "FREQ"}
	default:
		return nil
	}

	if len(args) < 2 || !slices.Contains(allowed, asciiUpper(args[1])) {
		return fmt.Errorf(
			"studio: %s %s is not allowed (only %s)",
			cmd,
			subcommandOf(args),
			strings.Join(allowed, ", "),
		)
	}

	return nil
}

func subcommandOf(args []string) string {
	if len(args) < 2 {
		return ""
	}

	return asciiUpper(args[1])
}

// asciiUpper upper-cases s for the allow list. The server reads command names
// in ASCII only, so a name with another character ("ınfo", which Unicode
// upper-cases to INFO) never matches a listed command.
func asciiUpper(s string) string {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return s
		}
	}

	return strings.ToUpper(s)
}
