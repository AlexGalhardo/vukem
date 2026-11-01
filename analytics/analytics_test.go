package analytics_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/analytics"
	"github.com/AlexGalhardo/vukem/config"
)

func TestOffWhenUnset(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[analytics.Config](config.MapSource(nil))
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	if got := cfg.Tags("n0nce"); got != "" {
		t.Errorf("Tags() = %q, want empty", got)
	}

	if got := cfg.CSPSources(); got != nil {
		t.Errorf("CSPSources() = %v, want nil", got)
	}

	if cfg.Enabled() {
		t.Error("Enabled() = true, want false")
	}
}

func checkRefused(t *testing.T, tests map[string]analytics.Config) {
	t.Helper()

	for name := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := tests[name].Validate(); err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}
