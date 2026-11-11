package studio

import (
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// sqlToken is one token of the reference scanner below, written apart from
// lex: kinds are 'w' (an unquoted word, lower case), 'q' (a double-quoted or
// backtick-quoted name, lower case), 's' (a string or dollar-quoted string),
// 'c' (a comment), ';' and 'o' (any other character that is not blank).
type sqlToken struct {
	kind byte
	text string
}

// scanSQL is the oracle's reading of text for family: the same lexical rules
// as lex (MySQL backslash escapes and "-- " comments, "/*!" and unterminated
// quotes refused), over a token list instead of a state machine.
func scanSQL(text, family string) ([]sqlToken, bool) {
	mysql := family == "mysql"

	var toks []sqlToken

	for i := 0; i < len(text); {
		c := text[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(text[i:], "--") && (!mysql || i+2 >= len(text) || text[i+2] <= ' '):
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i - 1
			}

			toks = append(toks, sqlToken{'c', ""})
			i += end + 1
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 || strings.HasPrefix(text[i:], "/*!") {
				return nil, false
			}

			toks = append(toks, sqlToken{'c', ""})
			i += 2 + end + 2
		case c == '\'' || c == '"' || c == '`':
			j := i + 1

			for ; j < len(text) && text[j] != c; j++ {
				if mysql && text[j] == '\\' {
					j++
				}
			}

			if j >= len(text) {
				return nil, false
			}

			if c == '\'' {
				toks = append(toks, sqlToken{'s', ""})
			} else {
				toks = append(toks, sqlToken{'q', strings.ToLower(text[i+1 : j])})
			}

			i = j + 1
		case c == ';':
			toks = append(toks, sqlToken{';', ""})
			i++
		case c == '$':
			j := i + 1
			for j < len(text) && (text[j] == '_' || text[j]|0x20 >= 'a' && text[j]|0x20 <= 'z') {
				j++
			}

			if j < len(text) && text[j] == '$' {
				tag := text[i : j+1]

				end := strings.Index(text[j+1:], tag)
				if end < 0 {
					return nil, false
				}

				toks = append(toks, sqlToken{'s', ""})
				i = j + 1 + end + len(tag)

				break
			}

			toks = append(toks, sqlToken{'o', "$"})
			i++
		case c == '_' || c|0x20 >= 'a' && c|0x20 <= 'z':
			j := i + 1
			for j < len(text) && (text[j] == '_' || text[j] == '$' || text[j]|0x20 >= 'a' && text[j]|0x20 <= 'z' ||
				text[j] >= '0' && text[j] <= '9') {
				j++
			}

			toks = append(toks, sqlToken{'w', strings.ToLower(text[i:j])})
			i = j
		default:
			toks = append(toks, sqlToken{'o', string(c)})
			i++
		}
	}

	return toks, true
}

var fuzzFamilies = []string{"postgres", "mysql", "sqlite"}

// FuzzReadOnlyStatement checks the console's classifier and statement splitter
// against the reference scanner: they never panic, and a text accepted as a
// read is one statement (only comments after a semicolon), starts with a read
// verb, has no INTO and calls no denied function.
func FuzzReadOnlyStatement(f *testing.F) {
	for _, s := range []string{
		"SELECT 1", "select 1;", "select 1; select 2", "select 1;--x", "select 1 ; /* c */ ;",
		"select 'a;b'", "select 'unterminated", "select \"a;b\"", "select `a;b`", "select 'a\\'; drop table t; --'",
		"select 1 /*! into t */", "select 1 /*", "select /* ; */ 1", "select $$;$$", "select $tag$; drop table t$tag$",
		"select $tag$ unclosed", "select $1, $2", "select 1--1; drop table t", "select 1 --x\n; drop table t",
		"select 1 -- ;", "select 1 --; drop table t", "select\x00 1;\x00", "\x00select 1", "select 1;\x00",
		"select pg_sleep(1)", "select \"PG_SLEEP\"(1)", "select pg_sleep /* c */ (1)", "select sleep--1\n(5)",
		"select `sleep`(5)", "select sleep (5)", "select 1 into t", "select 'into'", "select \"into\"",
		"pragma table_info(t)", "(select 1)", "((with a as (select 1) select * from a))", "select é;ü",
		"select ' '; drop table t", "SELECT 1\u0085; drop table t", "select 1; drop table t",
		"select e'\\'' ; drop table t; select 'a'", "select 1 /* /* */ ; drop table t */",
		"explain select 1", "show tables", "table t", "values (1)", "desc t", "",
	} {
		for fam := range fuzzFamilies {
			f.Add(s, uint8(fam))
		}
	}

	f.Fuzz(func(t *testing.T, text string, fam uint8) {
		family := fuzzFamilies[int(fam)%len(fuzzFamilies)]
		mysql := family == "mysql"

		single := singleStatement(text, mysql)
		read := readOnlyStatement(text, family)

		if read && !single {
			t.Fatalf("readOnlyStatement(%q, %s) accepts what singleStatement refuses", text, family)
		}

		if !single {
			return
		}

		toks, ok := scanSQL(text, family)
		if !ok {
			t.Fatalf("lex accepts %q (%s), which the reference scanner cannot follow", text, family)
		}

		// One statement: after the first semicolon only comments may follow.
		semi := slices.IndexFunc(toks, func(k sqlToken) bool { return k.kind == ';' })
		if semi >= 0 {
			for _, k := range toks[semi+1:] {
				if k.kind != 'c' && k.kind != ';' {
					t.Fatalf(
						"singleStatement(%q, %v) accepts a second statement: %q after the semicolon",
						text,
						mysql,
						k,
					)
				}
			}
		}

		if !read {
			return
		}

		first := ""

		for i, k := range toks {
			if k.kind == 'w' && first == "" {
				first = k.text
			}

			if k.kind == 'w' && k.text == "into" {
				t.Fatalf("readOnlyStatement(%q, %s) accepts INTO", text, family)
			}

			if k.kind != 'w' && k.kind != 'q' {
				continue
			}

			// A name followed by "(" (comments in between) is a call.
			next := i + 1
			for next < len(toks) && toks[next].kind == 'c' {
				next++
			}

			if next < len(toks) && toks[next].kind == 'o' && toks[next].text == "(" && deniedFunction(k.text, family) {
				t.Fatalf("readOnlyStatement(%q, %s) accepts a call of %q", text, family, k.text)
			}
		}

		if !readVerbs[first] && (family != "sqlite" || first != "pragma") {
			t.Fatalf("readOnlyStatement(%q, %s) accepts the statement starting with %q", text, family, first)
		}
	})
}

// walkKeys visits every key of a decoded BSON value, however deeply nested.
func walkKeys(v any, visit func(key string)) {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			visit(e.Key)
			walkKeys(e.Value, visit)
		}
	case bson.A:
		for _, e := range x {
			walkKeys(e, visit)
		}
	case bson.M:
		for k, e := range x {
			visit(k)
			walkKeys(e, visit)
		}
	}
}

// FuzzMongoFilter checks the grid's filter builder: it never panics, and an
// accepted filter holds only the fields that were sent (a valid path), the
// comparison and $regex operators and $and, with scalar values and no object
// or array a client could use to smuggle an operator.
func FuzzMongoFilter(f *testing.F) {
	for _, s := range [][3]string{
		{"name", "eq", "ana"},
		{"age", "gt", "18"},
		{"_id", "eq", "507f1f77bcf86cd799439011"},
		{"name", "contains", ".*"},
		{"name", "starts", "$where"},
		{"$where", "eq", "1"},
		{"a.b", "eq", `{"$gt": 1}`},
		{"a", "eq", "[1]"},
		{"a", "eq", " {"},
		{"a", "$ne", "1"},
		{"a", "null", ""},
		{"a", "notnull", ""},
		{"", "eq", "x"},
		{"a..b", "eq", "x"},
		{"a.$b", "eq", "x"},
		{"a\x00", "eq", "x"},
		{"a", "eq", `"quoted"`},
		{"a", "eq", "1e400"},
		{"a", "eq", "true"},
		{"a", "eq", "\x00"},
		{"é", "eq", "x"},
		{"a", "eq", `{"$regex": "x"}`},
		{"a", "eq", "9223372036854775808"},
	} {
		f.Add(s[0], s[1], s[2], s[0], s[1], s[2])
	}

	f.Fuzz(func(t *testing.T, c1, op1, v1, c2, op2, v2 string) {
		filters := []Filter{{c1, op1, v1}, {c2, op2, v2}}

		for n := range 3 {
			got, err := buildMongoFilter(filters[:n])
			if err != nil {
				continue
			}

			columns := map[string]bool{}
			for _, fl := range filters[:n] {
				columns[fl.Column] = true
			}

			allowed := map[string]bool{
				"$and": true, "$eq": true, "$ne": true, "$lt": true, "$lte": true, "$gt": true,
				"$gte": true, "$regex": true,
			}

			walkKeys(got, func(key string) {
				if !allowed[key] && !columns[key] {
					t.Fatalf(
						"buildMongoFilter(%q) built the key %q, which was neither sent nor an allowed operator",
						filters[:n],
						key,
					)
				}

				if !allowed[key] && !fieldRe.MatchString(key) {
					t.Fatalf("buildMongoFilter(%q) built the invalid field %q", filters[:n], key)
				}
			})

			checkScalars(t, got, filters[:n])
		}
	})
}

// checkScalars fails when a value of an accepted filter is an object, an
// array or a type the builder never produces.
func checkScalars(t *testing.T, v any, filters []Filter) {
	t.Helper()

	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			switch e.Value.(type) {
			case bson.D, bson.A:
				checkScalars(t, e.Value, filters)
			case nil, string, bool, int64, float64, bson.ObjectID:
			default:
				t.Fatalf("buildMongoFilter(%q) put a %T value under %q", filters, e.Value, e.Key)
			}
		}
	case bson.A:
		for _, e := range x {
			checkScalars(t, e, filters)
		}
	}
}

// FuzzMongoConsole checks the console's decoders: they never panic; a
// document or pipeline accepted for a read-only console holds no JavaScript
// operator and no $out or $merge anywhere; a pipeline is an array of objects
// and an accepted _id is a scalar.
func FuzzMongoConsole(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"a": 1}`, `{"$where": "1"}`, `{"a": {"$function": {"body": "x"}}}`, `[{"$match": {"a": 1}}]`,
		`[{"$out": "x"}]`, `[{"$merge": {"into": "x"}}]`, `[{"$group": {"_id": 1, "v": {"$accumulator": {}}}}]`,
		`[]`, `[1]`, `[{}] , "x": 1`, `[{}]}, {"q": 1`, `{"a": {"$oid": "507f1f77bcf86cd799439011"}}`,
		`{"$code": "x"}`, `{"$code": "x", "$scope": {"$where": 1}}`, `{"a": [{"$where": 1}]}`, `"x"`, `{"_id": {"a": 1}}`,
		`{"$numberLong": "1"}`, `{"a": {"$date": 1}}`, "{\"a\": \"\x00\"}", `{"a": {"$regularExpression": {"pattern": "x", "options": ""}}}`,
		`{"a": 1e999}`, `{`, ``, `[{"$match": {"$expr": {"$function": {}}}}]`,
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		forbidden := func(key string) {
			if blockedAlways[key] || writeStages[key] {
				t.Fatalf("a read-only console accepted %q, which holds the key %q", src, key)
			}
		}

		if doc, err := decodeConsoleDoc(src); err == nil && checkConsoleValue(doc, false) == nil {
			walkKeys(doc, forbidden)
		}

		if stages, err := decodePipeline(src); err == nil {
			for _, s := range stages {
				if _, ok := s.(bson.D); !ok {
					t.Fatalf("decodePipeline(%q) accepted a stage that is %T", src, s)
				}
			}

			if checkConsoleValue(stages, false) == nil {
				walkKeys(stages, forbidden)
			}

			// With --write only the JavaScript operators stay refused.
			if checkConsoleValue(stages, true) == nil {
				walkKeys(stages, func(key string) {
					if blockedAlways[key] {
						t.Fatalf("a write console accepted %q, which holds the key %q", src, key)
					}
				})
			}
		}

		if id, err := parseID(src); err == nil {
			switch id.(type) {
			case bson.ObjectID, string, int32, int64, float64, bson.Binary, bson.DateTime, bson.Decimal128:
			default:
				t.Fatalf("parseID(%q) accepted a %T", src, id)
			}
		}
	})
}

// quoteRedisArg is the inverse of parseRedisLine's quoting.
func quoteRedisArg(a string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
}

// FuzzRedisLine checks the console's command line: the parser never panics,
// its result survives being quoted and parsed again, and a command the check
// accepts is, spelled in ASCII, on the allow list (a write command only with
// the write flag, MEMORY and OBJECT only with a read subcommand).
func FuzzRedisLine(f *testing.F) {
	for _, s := range []string{
		"GET foo", `SET "a b" "c\"d"`, `GET "unterminated`, `get a\`, `"" ""`, `GET ""`, "  \t ", "",
		"keys *", "FLUSHALL", "MEMORY USAGE k", "memory doctor", "OBJECT ENCODING k", "object freq", "object",
		"EVAL \"return 1\" 0", "ınfo", "ſet a b", "pıng", "GET \x00", "GET é", " GET a", "GET a",
		`GET "a\\"`, `GET "a\\\"`, "DEBUG SLEEP 1", "CONFIG SET dir /tmp", "SCAN 0 MATCH *", "info\x00",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}

	f.Fuzz(func(t *testing.T, line string, write bool) {
		args, err := parseRedisLine(line)
		if err != nil {
			return
		}

		if len(args) == 0 {
			t.Fatalf("parseRedisLine(%q) = no arguments and no error", line)
		}

		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = quoteRedisArg(a)
		}

		again, err := parseRedisLine(strings.Join(quoted, " "))
		if err != nil || !slices.Equal(again, args) {
			t.Fatalf("parseRedisLine(%q) = %q, which does not survive quoting: %q, %v", line, args, again, err)
		}

		if checkRedisArgs(args, write) != nil {
			return
		}

		cmd := strings.ToUpper(args[0])
		if !isASCII(args[0]) {
			t.Fatalf("checkRedisArgs(%q, %v) accepts a command that is not ASCII: the server would not read it as %q",
				args, write, cmd)
		}

		switch {
		case slices.Contains(redisReadCommands, cmd):
		case write && slices.Contains(redisWriteCommands, cmd):
		default:
			t.Fatalf("checkRedisArgs(%q, %v) accepts %q, which is not allowed", args, write, cmd)
		}

		var sub []string

		switch cmd {
		case "MEMORY":
			sub = []string{"USAGE"}
		case "OBJECT":
			sub = []string{"ENCODING", "IDLETIME", "FREQ"}
		default:
			return
		}

		if len(args) < 2 || !isASCII(args[1]) || !slices.Contains(sub, strings.ToUpper(args[1])) {
			t.Fatalf("checkRedisArgs(%q, %v) accepts %s with a subcommand outside %v", args, write, cmd, sub)
		}
	})
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}

	return true
}
