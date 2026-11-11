package studio

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRedisParseLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line    string
		want    []string
		wantErr bool
	}{
		{"GET foo", []string{"GET", "foo"}, false},
		{"  SET   a   b  ", []string{"SET", "a", "b"}, false},
		{`SET k "hello world"`, []string{"SET", "k", "hello world"}, false},
		{`SET k "a \"quoted\" word"`, []string{"SET", "k", `a "quoted" word`}, false},
		{`SET k ""`, []string{"SET", "k", ""}, false},
		{"", nil, true},
		{"   ", nil, true},
		{`SET k "open`, nil, true},
	}

	for _, tt := range tests {
		got, err := parseRedisLine(tt.line)
		if (err != nil) != tt.wantErr || (err == nil && cmp.Diff(tt.want, got) != "") {
			t.Errorf("parseRedisLine(%q) = %q, %v, want %q (error %v)", tt.line, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestSecurity_A01_StudioRedisAllowsOnlyListedCommands(t *testing.T) {
	t.Parallel()

	for _, write := range []bool{false, true} {
		for _, cmd := range redisReadCommands {
			if err := checkRedisArgs([]string{cmd, "k"}, write); err != nil && cmd != "MEMORY" && cmd != "OBJECT" {
				t.Errorf("read command %s refused (write=%v): %v", cmd, write, err)
			}
		}

		for _, cmd := range []string{
			"KEYS", "FLUSHALL", "FLUSHDB", "CONFIG", "SHUTDOWN", "DEBUG", "EVAL", "EVALSHA", "SCRIPT", "MIGRATE",
			"REPLICAOF", "ACL", "CLIENT", "MONITOR", "SUBSCRIBE", "keys", "flushall", "Keys", "FUNCTION", "FCALL",
			"MODULE", "SAVE", "BGSAVE", "SLAVEOF", "RESET", "AUTH", "SWAPDB", "MOVE", "COPY", "WAIT", "FOOBAR",
		} {
			err := checkRedisArgs([]string{cmd, "k"}, write)
			if err == nil || !strings.Contains(err.Error(), "is not allowed in vukem-studio") {
				t.Errorf("%s (write=%v) = %v, want a refusal", cmd, write, err)
			}
		}
	}

	// A lower-case allowed command is accepted: the list is checked after
	// upper-casing, so "keys" is refused only because KEYS is not listed.
	if err := checkRedisArgs([]string{"get", "k"}, false); err != nil {
		t.Errorf("get = %v", err)
	}
}

func TestSecurity_A01_StudioRedisWriteCommandsNeedWriteFlag(t *testing.T) {
	t.Parallel()

	for _, cmd := range redisWriteCommands {
		if err := checkRedisArgs(
			[]string{cmd, "k", "v"},
			false,
		); err == nil ||
			!strings.Contains(err.Error(), "--write") {
			t.Errorf("%s without write = %v, want a refusal that mentions --write", cmd, err)
		}

		if err := checkRedisArgs([]string{cmd, "k", "v"}, true); err != nil {
			t.Errorf("%s with write = %v", cmd, err)
		}
	}

	// The two lists never overlap: a write command cannot hide among the reads.
	for _, w := range redisWriteCommands {
		if slices.Contains(redisReadCommands, w) {
			t.Errorf("%s is in both lists", w)
		}
	}
}

func TestSecurity_A01_StudioRedisSubcommandsRestricted(t *testing.T) {
	t.Parallel()

	refused := [][]string{
		{"MEMORY", "DOCTOR"},
		{"MEMORY", "PURGE"},
		{"MEMORY", "STATS"},
		{"MEMORY"},
		{"OBJECT", "HELP"},
		{"OBJECT"},
		{"object", "freq"}, // lower-case subcommand is fine, a missing key is not: checked below
	}

	for _, args := range refused[:6] {
		if err := checkRedisArgs(args, true); err == nil {
			t.Errorf("%v accepted", args)
		}
	}

	for _, args := range [][]string{
		{"MEMORY", "USAGE", "k"},
		{"memory", "usage", "k"},
		{"OBJECT", "ENCODING", "k"},
		{"OBJECT", "IDLETIME", "k"},
		{"OBJECT", "FREQ", "k"},
	} {
		if err := checkRedisArgs(args, false); err != nil {
			t.Errorf("%v refused: %v", args, err)
		}
	}
}

func TestRedisNeverAllowsKeys(t *testing.T) {
	t.Parallel()

	for _, list := range [][]string{redisReadCommands, redisWriteCommands} {
		for _, c := range list {
			if c == "KEYS" || c == "FLUSHALL" || c == "FLUSHDB" || c == "CONFIG" || c == "EVAL" {
				t.Errorf("%s is on an allow-list", c)
			}
		}
	}
}
