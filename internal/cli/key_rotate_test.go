package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func envValues(t *testing.T, dir string) map[string]string {
	t.Helper()

	f, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, err := config.ParseEnv(f)
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]string{}
	for _, e := range entries {
		out[e.Key] = e.Value
	}

	return out
}

func TestKeyRotate(t *testing.T) {
	t.Parallel()

	first, second := config.GenerateKey(), config.GenerateKey()

	tests := []struct {
		name     string
		env      string
		wantPrev []string
		wantTail string
	}{
		{
			name:     "no previous keys",
			env:      "APP_NAME=Blog\nAPP_KEY=" + first + "\nX=1\n",
			wantPrev: []string{first},
			wantTail: "\nX=1\n",
		},
		{
			name:     "existing previous keys, CRLF, quoted",
			env:      "APP_KEY=\"" + first + "\"\r\nAPP_PREVIOUS_KEYS=" + second + " , " + first + "\r\nX=1",
			wantPrev: []string{first, second},
			wantTail: "\r\nX=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tt.env), 0o600); err != nil {
				t.Fatal(err)
			}

			code, out, errOut := vukem(t, dir, "key:rotate")
			if code != 0 {
				t.Fatalf("key:rotate = %d %q", code, errOut)
			}

			if strings.Contains(out, first) || strings.Contains(out, second) {
				t.Errorf("key:rotate printed a key: %q", out)
			}

			got := envValues(t, dir)

			if _, err := config.ParseKey(config.NewSecret(got["APP_KEY"])); err != nil || got["APP_KEY"] == first {
				t.Fatalf("APP_KEY = %q, %v, want a new valid key", got["APP_KEY"], err)
			}

			if want := strings.Join(tt.wantPrev, ","); got["APP_PREVIOUS_KEYS"] != want {
				t.Errorf("APP_PREVIOUS_KEYS = %q, want %q", got["APP_PREVIOUS_KEYS"], want)
			}

			if raw := read(t, dir, ".env"); !strings.HasSuffix(raw, tt.wantTail) {
				t.Errorf(".env = %q, want the other lines kept", raw)
			}
		})
	}
}

func TestKeyRotateRefuses(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]string{
		"no .env":         "",
		"no APP_KEY":      "APP_NAME=Blog\n",
		"empty APP_KEY":   "APP_KEY=\n",
		"invalid APP_KEY": "APP_KEY=base64:YWJj\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if env != "" {
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if code, _, errOut := vukem(t, dir, "key:rotate"); code == 0 || !strings.Contains(errOut, "APP_KEY") {
				t.Fatalf("key:rotate = %d %q, want an APP_KEY error", code, errOut)
			}

			if env != "" && read(t, dir, ".env") != env {
				t.Error("refused key:rotate changed .env")
			}
		})
	}
}

func TestKeyRotateKeepsLineSyntax(t *testing.T) {
	t.Parallel()

	first, second := config.GenerateKey(), config.GenerateKey()

	dir := t.TempDir()
	src := "  export APP_KEY=" + first + " # rotated yearly\n" +
		"export APP_PREVIOUS_KEYS=\"" + second + "\" # old keys\nX=1\n"

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "key:rotate"); code != 0 {
		t.Fatalf("key:rotate = %d %q", code, errOut)
	}

	got := envValues(t, dir)
	lines := strings.Split(read(t, dir, ".env"), "\n")

	if want := "  export APP_KEY=" + got["APP_KEY"] + " # rotated yearly"; lines[0] != want {
		t.Errorf("APP_KEY line = %q, want %q", lines[0], want)
	}

	if want := "export APP_PREVIOUS_KEYS=" + first + "," + second + " # old keys"; lines[1] != want {
		t.Errorf("APP_PREVIOUS_KEYS line = %q, want %q", lines[1], want)
	}

	// A missing APP_PREVIOUS_KEYS line follows the APP_KEY line's prefix.
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("export APP_KEY="+first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "key:rotate"); code != 0 {
		t.Fatalf("key:rotate = %d %q", code, errOut)
	}

	if raw := read(t, dir, ".env"); !strings.HasSuffix(raw, "\nexport APP_PREVIOUS_KEYS="+first+"\n") {
		t.Errorf(".env = %q, want an exported APP_PREVIOUS_KEYS line", raw)
	}
}

func TestKeyRotateValidatesPreviousKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := "APP_KEY=" + config.GenerateKey() + "\nAPP_PREVIOUS_KEYS=" + config.GenerateKey() + ",base64:YWJj\n"

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := vukem(t, dir, "key:rotate")
	if code == 0 || !strings.Contains(errOut, "APP_PREVIOUS_KEYS entry 2") {
		t.Fatalf("key:rotate with a malformed previous key = %d %q, want an entry 2 error", code, errOut)
	}

	if read(t, dir, ".env") != env {
		t.Error("refused key:rotate changed .env")
	}
}

func TestKeyRotateWarnsOnLongPreviousList(t *testing.T) {
	t.Parallel()

	prev := []string{config.GenerateKey(), config.GenerateKey(), config.GenerateKey()}
	current := config.GenerateKey()

	dir := t.TempDir()
	env := "APP_KEY=" + current + "\nAPP_PREVIOUS_KEYS=" + strings.Join(prev, ",") + "\n"

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := vukem(t, dir, "key:rotate")
	if code != 0 || !strings.Contains(errOut, "4 keys") {
		t.Fatalf("key:rotate = %d %q, want a warning about 4 previous keys", code, errOut)
	}

	for _, k := range append(prev, current) {
		if strings.Contains(out+errOut, k) {
			t.Fatalf("key:rotate output holds a key: %q %q", out, errOut)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	noTemp := func(t *testing.T, dir string) {
		t.Helper()

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".env.tmp-") {
				t.Errorf("temp file %s left behind", e.Name())
			}
		}
	}

	t.Run("replaces the file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		p := filepath.Join(dir, ".env")

		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := writeFileAtomic(p, []byte("new")); err != nil {
			t.Fatal(err)
		}

		if got := read(t, dir, ".env"); got != "new" {
			t.Errorf(".env = %q, want new", got)
		}

		noTemp(t, dir)
	})

	t.Run("follows a symlink", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(dir, "shared.env")
		link := filepath.Join(dir, ".env")

		if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err) // Windows without developer mode
		}

		if err := writeFileAtomic(link, []byte("new")); err != nil {
			t.Fatal(err)
		}

		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf(".env is no longer a symlink: %v, %v", fi, err)
		}

		if got := read(t, dir, "shared.env"); got != "new" {
			t.Errorf("symlink target = %q, want new", got)
		}
	})

	t.Run("failed rename removes the temp file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(dir, ".env")

		// A non-empty directory cannot be replaced by a file on any OS.
		if err := os.MkdirAll(filepath.Join(target, "keep"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := writeFileAtomic(target, []byte("new")); err == nil {
			t.Fatal("writeFileAtomic(over a directory) error = nil, want an error")
		}

		noTemp(t, dir)
	})
}
