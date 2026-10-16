package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Env is the application environment (APP_ENV).
type Env string

// Known environments.
const (
	Development Env = "development"
	Production  Env = "production"
	Test        Env = "test"
)

// filePrefix marks a value read from a file (Docker/Kubernetes secrets).
// It is "@file:" rather than "file:" so SQLite DSNs such as
// "file:storage/app.db" stay untouched.
const filePrefix = "@file:"

// Source is a resolved set of configuration values.
type Source struct {
	values map[string]string
	env    Env
}

// Lookup returns the value for key and whether it is set.
func (s *Source) Lookup(key string) (string, bool) {
	v, ok := s.values[key]
	return v, ok
}

// Get returns the value for key or "" when unset.
func (s *Source) Get(key string) string { return s.values[key] }

// Env returns the resolved application environment.
func (s *Source) Env() Env { return s.env }

// Keys returns the sorted keys of every resolved value.
func (s *Source) Keys() []string { return slices.Sorted(maps.Keys(s.values)) }

// MapSource builds a Source from a map, typically in tests.
func MapSource(m map[string]string) *Source {
	values := maps.Clone(m)
	if values == nil {
		values = map[string]string{}
	}

	return &Source{values: values, env: envOf(values["APP_ENV"])}
}

// LoadSource reads .env files from fsys and merges them with the process
// environment provided by getenv (use os.LookupEnv in production code).
// readFile resolves "@file:" values; nil disables them.
func LoadSource(
	fsys fs.FS,
	getenv func(string) (string, bool),
	readFile func(string) ([]byte, error),
) (*Source, error) {
	base, err := readEnvFile(fsys, ".env")
	if err != nil {
		return nil, err
	}

	appEnv, ok := getenv("APP_ENV")
	if !ok {
		appEnv = rawValue(base, "APP_ENV")
	}

	env := envOf(appEnv)

	// Lowest precedence first; .env.local is skipped in tests for reproducibility.
	var files []string
	if env == Test {
		files = []string{".env.test", ".env.test.local"}
	} else {
		files = []string{".env." + string(env), ".env.local", ".env." + string(env) + ".local"}
	}

	merged := map[string]Entry{}
	for _, e := range base {
		merged[e.Key] = e
	}

	for _, name := range files {
		entries, err := readEnvFile(fsys, name)
		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			merged[e.Key] = e
		}
	}

	values := make(map[string]string, len(merged))

	lookup := func(k string) (string, bool) {
		if v, ok := getenv(k); ok {
			return v, true
		}

		e, ok := merged[k]
		if !ok {
			return "", false
		}

		return unescape(e.Value), true
	}

	for k, e := range merged {
		if v, ok := getenv(k); ok {
			values[k] = v
			continue
		}

		v := e.Value
		if e.Expand {
			v = expand(v, lookup)
		}

		if strings.HasPrefix(v, filePrefix) {
			if readFile == nil {
				return nil, fmt.Errorf("config: %s uses %q but file references are disabled", k, filePrefix)
			}

			data, err := readFile(strings.TrimPrefix(v, filePrefix))
			if err != nil {
				return nil, fmt.Errorf("config: read %s: %w", k, err)
			}

			v = strings.TrimRight(string(data), "\r\n")
		}

		values[k] = v
	}

	if _, ok := values["APP_ENV"]; !ok {
		if v, ok := getenv("APP_ENV"); ok {
			values["APP_ENV"] = v
		}
	}

	return &Source{values: values, env: env}, nil
}

func readEnvFile(fsys fs.FS, name string) ([]Entry, error) {
	f, err := fsys.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", name, err)
	}
	defer f.Close()

	entries, err := ParseEnv(f)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", name, err)
	}

	return entries, nil
}

func rawValue(entries []Entry, key string) string {
	v := ""
	for _, e := range entries {
		if e.Key == key {
			v = e.Value
		}
	}

	return v
}

func envOf(s string) Env {
	if s == "" {
		return Development
	}

	return Env(strings.ToLower(s))
}

// expand replaces ${VAR} and $VAR references; \$ yields a literal "$".
// Unknown variables expand to "".
func expand(s string, lookup func(string) (string, bool)) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '$':
			b.WriteByte('$')
			i++
		case c == '$' && i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}

			v, _ := lookup(s[i+2 : i+end])
			b.WriteString(v)
			i += end
		case c == '$' && i+1 < len(s) && isNameStart(s[i+1]):
			j := i + 1
			for j < len(s) && isNameChar(s[j]) {
				j++
			}

			v, _ := lookup(s[i+1 : j])
			b.WriteString(v)
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

func unescape(s string) string { return strings.ReplaceAll(s, `\$`, "$") }

func isNameStart(c byte) bool { return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

func isNameChar(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }
