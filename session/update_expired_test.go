package session

import (
	"errors"
	"testing"
	"time"
)

// A session past its expiry that Prune has not removed yet must not be
// brought back by Update.
func TestUpdateDoesNotResurrectExpiredSession(t *testing.T) {
	t.Parallel()

	for name, store := range storesUnderTest(t) {
		us, ok := store.(UserStore)
		if !ok {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if c, ok := store.(clocked); ok {
				c.setClock(func() time.Time { return now })
			}

			if err := us.Create(t.Context(), "sid", "7", []byte(`{}`), now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}

			now = now.Add(2 * time.Hour)

			err := us.Update(t.Context(), "sid", "7", []byte(`{"v":2}`), now.Add(time.Hour))
			if !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("Update(expired) = %v, want ErrSessionNotFound", err)
			}

			if _, found, err := store.Load(t.Context(), "sid"); err != nil || found {
				t.Fatalf("Load() after Update(expired) = found %v, %v; want not found", found, err)
			}
		})
	}
}
