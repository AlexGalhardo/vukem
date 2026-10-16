package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type level int

func (l *level) UnmarshalText(b []byte) error {
	switch string(b) {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return fmt.Errorf("bad level %q", b)
	}
	return nil
}

type nested struct {
	Host string `env:"DB_HOST" default:"localhost"`
	Port int    `env:"DB_PORT" default:"5432"`
}

type sample struct {
	Name     string        `env:"APP_NAME"  default:"Lumek"`
	Debug    bool          `env:"APP_DEBUG"`
	Port     int           `env:"PORT"      default:"3000"`
	Workers  uint8         `env:"WORKERS"`
	Ratio    float64       `env:"RATIO"`
	Timeout  time.Duration `env:"TIMEOUT"   default:"5s"`
	Tags     []string      `env:"TAGS"`
	URL      *url.URL      `env:"APP_URL"`
	Key      Secret        `env:"APP_KEY"`
	Env      Env           `env:"APP_ENV"   default:"development"`
	Level    level         `env:"LEVEL"     default:"low"`
	DB       nested
	Ignored  string
	internal string `env:"INTERNAL"` //nolint:unused // verifies unexported fields are skipped
}

func TestFill(t *testing.T) {
	t.Parallel()

	src := MapSource(map[string]string{
		"APP_DEBUG": "true",
		"PORT":      "8080",
		"WORKERS":   "4",
		"RATIO":     "0.5",
		"TAGS":      "a, b,,c",
		"APP_URL":   "https://example.com/x",
		"APP_KEY":   "secret-value",
		"APP_ENV":   "production",
		"LEVEL":     "high",
		"DB_HOST":   "db",
		"INTERNAL":  "x",
	})

	var got sample
	if err := Fill(&got, src); err != nil {
		t.Fatalf("Fill() error = %v", err)
	}

	if got.Name != "Lumek" || !got.Debug || got.Port != 8080 || got.Workers != 4 || got.Ratio != 0.5 {
		t.Errorf("Fill() scalars = %+v", got)
	}
	if got.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", got.Timeout)
	}
	if diff := cmp.Diff([]string{"a", "b", "c"}, got.Tags); diff != "" {
		t.Errorf("Tags mismatch (-want +got):\n%s", diff)
	}
	if got.URL == nil || got.URL.Host != "example.com" {
		t.Errorf("URL = %v", got.URL)
	}
	if got.Key.Reveal() != "secret-value" || got.Env != Production || got.Level != 2 {
		t.Errorf("Key/Env/Level = %q %q %d", got.Key.Reveal(), got.Env, got.Level)
	}
	if got.DB.Host != "db" || got.DB.Port != 5432 {
		t.Errorf("DB = %+v", got.DB)
	}
}

func TestFillAggregatesErrors(t *testing.T) {
	t.Parallel()

	src := MapSource(map[string]string{"PORT": "abc", "APP_DEBUG": "maybe", "LEVEL": "mid", "WORKERS": "999"})

	var got sample
	err := Fill(&got, src)
	if err == nil {
		t.Fatal("Fill() error = nil, want errors")
	}

	for _, want := range []string{"PORT", "APP_DEBUG", "LEVEL", "WORKERS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Fill() error %q does not mention %s", err, want)
		}
	}
}

func TestFillRejectsNonPointer(t *testing.T) {
	t.Parallel()

	if err := Fill(sample{}, MapSource(nil)); err == nil {
		t.Fatal("Fill(non-pointer) error = nil")
	}
}

func TestLoadGeneric(t *testing.T) {
	t.Parallel()

	got, err := Load[nested](MapSource(map[string]string{"DB_PORT": "1"}))
	if err != nil || got.Port != 1 || got.Host != "localhost" {
		t.Fatalf("Load() = %+v, %v", got, err)
	}
}

func TestSecurity_A04_SecretRedaction(t *testing.T) {
	t.Parallel()

	s := NewSecret("hunter2")

	checks := map[string]string{
		"String":   s.String(),
		"Sprintf":  fmt.Sprintf("%v %s %+v", s, s, struct{ S Secret }{s}),
		"GoString": fmt.Sprintf("%#v", s),
	}

	b, err := json.Marshal(struct {
		S Secret `json:"s"`
	}{s})
	if err != nil {
		t.Fatal(err)
	}
	checks["JSON"] = string(b)

	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "key", s)
	checks["slog"] = buf.String()

	for name, out := range checks {
		if strings.Contains(out, "hunter2") {
			t.Errorf("%s leaked the secret: %q", name, out)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%s = %q, want [redacted]", name, out)
		}
	}

	if s.Reveal() != "hunter2" || s.IsZero() || !(Secret{}).IsZero() {
		t.Error("Reveal/IsZero wrong")
	}
}
