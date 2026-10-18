package postgres

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/sse"
)

// The Hub accepts any sse.Broker, so the compiler checks the shape here
// instead of every app discovering a mismatch at wiring time.
var _ sse.Broker = (*Broker)(nil)

// A broker on SQLite would accept every publish and deliver nothing.
// Failing at construction turns a silent production fault into a boot error.
func TestNewBrokerRefusesAnythingThatIsNotPostgres(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "not-postgres.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if _, err := NewBroker(db); !errors.Is(err, ErrNotPostgres) {
		t.Fatalf("NewBroker on SQLite = %v, want ErrNotPostgres", err)
	}

	if _, err := NewBroker(nil); !errors.Is(err, ErrNotPostgres) {
		t.Fatalf("NewBroker(nil) = %v, want ErrNotPostgres", err)
	}
}

// The channel name reaches LISTEN as an identifier, never as text, so it
// must come out of notifyIdentifier as something that cannot carry quoting,
// cannot pass the 63-byte identifier limit and cannot change case when
// PostgreSQL folds an unquoted identifier.
func TestNotifyIdentifierCannotCarryQuotingOrOverflow(t *testing.T) {
	t.Parallel()

	safe := regexp.MustCompile(`^vukem_[0-9a-f]{32}$`)

	for _, channel := range []string{
		"",
		"sse:posts",
		`sse:"; DROP TABLE users; --`,
		"sse:" + strings.Repeat("a", 500),
		"SSE:Posts",
	} {
		got := notifyIdentifier(channel)

		if !safe.MatchString(got) {
			t.Errorf("notifyIdentifier(%q) = %q, want vukem_ plus 32 lowercase hex digits", channel, got)
		}

		if len(got) > maxIdentifier {
			t.Errorf("notifyIdentifier(%q) is %d bytes, over the %d-byte limit", channel, len(got), maxIdentifier)
		}

		if again := notifyIdentifier(channel); again != got {
			t.Errorf("notifyIdentifier(%q) = %q then %q: LISTEN and NOTIFY would disagree", channel, got, again)
		}
	}

	// Case matters: "SSE:Posts" and "sse:posts" are different topics, and an
	// identifier that folded them together would cross their streams.
	if notifyIdentifier("sse:posts") == notifyIdentifier("SSE:Posts") {
		t.Error("notifyIdentifier maps two different channels to one identifier")
	}
}
