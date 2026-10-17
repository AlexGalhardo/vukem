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
