package tests

import (
	"testing"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/examples/blog/bootstrap"
)

// TestViewCheck keeps the templates of the typed views in step with their
// Go types, the routes and the htmx targets.
func TestViewCheck(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"APP_KEY":      config.GenerateKey(),
		"APP_ENV":      "test",
		"DATABASE_URL": "file:views?mode=memory",
	}
	getenv := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}

	if err := vukem.RunMain(t.Context(), []string{"app", "view:check"}, getenv, bootstrap.New); err != nil {
		t.Fatal(err)
	}
}
