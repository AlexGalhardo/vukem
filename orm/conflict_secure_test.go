package orm

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A10_ConflictResponseHidesDriverText(t *testing.T) {
	t.Parallel()

	cause := errors.New(
		`ERROR: duplicate key value violates unique constraint "users_email_key" (Key (email)=(ana@secret.example) already exists)`,
	)

	for name, ex := range map[string]database.Executor{
		"driver decides": uniqueEx{fakeEx{database.SQLite}, true},
		"text match":     fakeEx{database.SQLite},
	} {
		err := mapError(ex, cause)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: mapError = %v, want ErrConflict", name, err)
		}

		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/users", nil)
		r.Header.Set("Accept", "application/json")

		rec := httptest.NewRecorder()
		web.NewCtx(rec, r, &web.Services{Log: slog.New(slog.DiscardHandler)}).Serve(func(*web.Ctx) error {
			return err
		})

		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", name, rec.Code)
		}

		for _, leak := range []string{"ana@secret.example", "users_email_key", "duplicate key"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: the driver text %q reached the client: %s", name, leak, rec.Body.String())
			}
		}
	}
}
