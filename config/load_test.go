package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

func fakeEnv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadSourcePrecedence(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		".env":                   {Data: []byte("A=env\nB=env\nC=env\nD=env\nE=env\nAPP_ENV=production")},
		".env.production":        {Data: []byte("B=prod\nC=prod\nD=prod")},
		".env.local":             {Data: []byte("C=local\nD=local")},
		".env.production.local":  {Data: []byte("D=prodlocal")},
		".env.test":              {Data: []byte("A=test")},
		".env.test.local":        {Data: []byte("A=testlocal")},
		".env.development.local": {Data: []byte("A=devlocal")},
	}

	src, err := LoadSource(fsys, fakeEnv(map[string]string{"E": "process"}), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"A":       "env",
		"B":       "prod",
		"C":       "local",
		"D":       "prodlocal",
		"E":       "process",
		"APP_ENV": "production",
	}
	for k, v := range want {
		if got, _ := src.Lookup(k); got != v {
			t.Errorf("Lookup(%q) = %q, want %q", k, got, v)
		}
	}

	if src.Env() != Production {
		t.Errorf("Env() = %q, want production", src.Env())
	}
}

func TestLoadSourceTestEnvSkipsLocal(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		".env":            {Data: []byte("A=env")},
		".env.local":      {Data: []byte("A=local")},
		".env.test":       {Data: []byte("B=test")},
		".env.test.local": {Data: []byte("C=testlocal")},
	}

	src, err := LoadSource(fsys, fakeEnv(map[string]string{"APP_ENV": "test"}), nil)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{"A": "env", "B": "test", "C": "testlocal"} {
		if got, _ := src.Lookup(k); got != v {
			t.Errorf("Lookup(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestLoadSourceDefaultsToDevelopmentAndMissingFiles(t *testing.T) {
	t.Parallel()

	src, err := LoadSource(fstest.MapFS{}, fakeEnv(map[string]string{"ONLY_PROCESS": "1"}), nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := src.Get("ONLY_PROCESS"); got != "1" {
		t.Errorf("Get(ONLY_PROCESS) = %q, want process-only variable", got)
	}

	if src.Env() != Development {
		t.Errorf("Env() = %q, want development", src.Env())
	}

	if _, ok := src.Lookup("NOPE"); ok {
		t.Error("Lookup(NOPE) found a value")
	}
}

func TestLoadSourceExpansionAndFileRefs(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		".env": {Data: []byte(strings.Join([]string{
			"HOST=localhost",
			"URL=http://${HOST}:$PORT/x",
			"LITERAL='${HOST}'",
			`ESCAPED="cost \$5"`,
			"MISSING=a${NOPE}b",
			"PASS=@file:secrets/db",
			"SQLITE=file:storage/db.sqlite",
		}, "\n"))},
	}
	readFile := func(name string) ([]byte, error) {
		if name == "secrets/db" {
			return []byte("s3cret\n"), nil
		}
		return nil, fs.ErrNotExist
	}

	src, err := LoadSource(fsys, fakeEnv(map[string]string{"PORT": "8080"}), readFile)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"URL":     "http://localhost:8080/x",
		"LITERAL": "${HOST}",
		"ESCAPED": "cost $5",
		"MISSING": "ab",
		"PASS":    "s3cret",
		"SQLITE":  "file:storage/db.sqlite",
	}
	for k, v := range want {
		if got, _ := src.Lookup(k); got != v {
			t.Errorf("Lookup(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestLoadSourceErrors(t *testing.T) {
	t.Parallel()

	_, err := LoadSource(fstest.MapFS{".env": {Data: []byte("BAD")}}, fakeEnv(nil), nil)
	if err == nil || !strings.Contains(err.Error(), ".env: line 1") {
		t.Fatalf("LoadSource() error = %v, want parse error naming the file", err)
	}

	fsys := fstest.MapFS{".env": {Data: []byte("P=@file:missing")}}
	readFile := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	_, err = LoadSource(fsys, fakeEnv(nil), readFile)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LoadSource() error = %v, want fs.ErrNotExist", err)
	}
}

func TestMapSource(t *testing.T) {
	t.Parallel()

	src := MapSource(map[string]string{"APP_ENV": "test", "X": "1"})
	if src.Env() != Test {
		t.Errorf("Env() = %q, want test", src.Env())
	}
	if diff := cmp.Diff([]string{"APP_ENV", "X"}, src.Keys()); diff != "" {
		t.Errorf("Keys() mismatch (-want +got):\n%s", diff)
	}
}
