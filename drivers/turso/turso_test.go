package turso

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	tursogo "turso.tech/database/tursogo"

	"github.com/AlexGalhardo/vukem/database"
)

func TestParseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "turso://storage/a.db", want: "storage/a.db"},
		{raw: "turso:///abs/a.db", want: "/abs/a.db"},
		{raw: "turso:///C:/data/a.db", want: "C:/data/a.db"},
		{raw: "turso://", wantErr: true},
		{raw: "turso://a.db?mode=ro", wantErr: true},
		{raw: "file:a.db", wantErr: true},
		{raw: "postgres://u:p@h/db", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parseURL(tt.raw)
		if tt.wantErr {
			if !errors.Is(err, ErrInvalidURL) || strings.Contains(err.Error(), tt.raw) {
				t.Errorf("parseURL(%q) = %q, %v, want ErrInvalidURL without the URL", tt.raw, got, err)
			}

			continue
		}

		if err != nil || got != tt.want {
			t.Errorf("parseURL(%q) = %q, %v, want %q", tt.raw, got, err, tt.want)
		}
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv(EnvRemoteURL, "libsql://app-org.turso.io")
	t.Setenv(EnvAuthToken, "secret")
	t.Setenv(EnvSyncInterval, "30s")

	o, err := OptionsFromEnv()
	if err != nil || o.RemoteURL != "libsql://app-org.turso.io" || o.AuthToken != "secret" ||
		o.SyncInterval != 30*time.Second {
		t.Fatalf("OptionsFromEnv = %+v, %v", o, err)
	}

	t.Setenv(EnvSyncInterval, "soon")

	if _, err := OptionsFromEnv(); err == nil || strings.Contains(err.Error(), "soon") {
		t.Errorf("OptionsFromEnv(bad interval) = %v, want an error", err)
	}

	t.Setenv(EnvSyncInterval, "")
	t.Setenv(EnvRemoteURL, "")
	t.Setenv(EnvAuthToken, "")

	if o, err := OptionsFromEnv(); err != nil || o.RemoteURL != "" {
		t.Errorf("OptionsFromEnv(nothing set) = %+v, %v, want a plain local file", o, err)
	}
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		o    Options
		want error
	}{
		{name: "local only", o: Options{}},
		{name: "libsql with token", o: Options{RemoteURL: "libsql://a.turso.io", AuthToken: "t"}},
		{name: "https with token", o: Options{RemoteURL: "https://a.turso.io", AuthToken: "t"}},
		{name: "no token", o: Options{RemoteURL: "libsql://a.turso.io"}, want: errNoToken},
		{
			name: "plaintext to a remote host",
			o:    Options{RemoteURL: "http://a.turso.io", AuthToken: "t"},
			want: errPlaintext,
		},
		{name: "plaintext to loopback", o: Options{RemoteURL: "http://127.0.0.1:8080", AuthToken: "t"}},
		{name: "plaintext to localhost", o: Options{RemoteURL: "http://localhost:8080", AuthToken: "t"}},
		{name: "unknown scheme", o: Options{RemoteURL: "ftp://a.turso.io", AuthToken: "t"}, want: errBadRemote},
		{name: "no host", o: Options{RemoteURL: "libsql://", AuthToken: "t"}, want: errBadRemote},
	}

	for _, tt := range tests {
		if err := tt.o.validate(); !errors.Is(err, tt.want) {
			t.Errorf("%s: validate() = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestDialectIsSQLiteFamilyUnderItsOwnName(t *testing.T) {
	t.Parallel()

	if Dialect.Name() != "turso" || Dialect.Placeholder(3) != "?" || Dialect.Quote("a") != `"a"` {
		t.Errorf("dialect = %q %q %q", Dialect.Name(), Dialect.Placeholder(3), Dialect.Quote("a"))
	}

	caps, ok := database.CapabilitiesOf(Dialect)
	if !ok || caps.Family != "sqlite" || !caps.Returning || caps.AdvisoryLock != "" || caps.MaxParams != 999 {
		t.Errorf("capabilities = %+v, %v", caps, ok)
	}
}

// fakeSync is the sync handle of the tests.
type fakeSync struct {
	pushes, pulls atomic.Int32
	pushErr       error
	pullErr       error
	lastPull      int64 // unix time of the last pull; 0 means never synced
	// block, when set, holds every Push until it is closed.
	block chan struct{}
}

func (f *fakeSync) Push(context.Context) error {
	f.pushes.Add(1)

	if f.block != nil {
		<-f.block
	}

	return f.pushErr
}

func (f *fakeSync) Pull(context.Context) (bool, error) {
	f.pulls.Add(1)
	return false, f.pullErr
}

func (f *fakeSync) LastPull(context.Context) (int64, error) { return f.lastPull, nil }

func syncing(sy syncer, interval time.Duration, buf *bytes.Buffer) *Database {
	d := New(Options{
		RemoteURL: "libsql://app-org.turso.io", AuthToken: "tok", SyncInterval: interval,
		Logger: slog.New(slog.NewTextHandler(buf, nil)),
	})
	d.sy = sy

	return d
}

func TestRunSyncsEveryIntervalAndSurvivesErrors(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		fake := &fakeSync{pushErr: errors.New("network down")}

		var logs bytes.Buffer

		d := syncing(fake, time.Second, &logs)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() { done <- d.Run(ctx) }()

		time.Sleep(3500 * time.Millisecond)
		synctest.Wait()

		if fake.pushes.Load() != 3 {
			t.Errorf(
				"pushes after 3.5s = %d, want 3 (one per interval, errors do not stop the loop)",
				fake.pushes.Load(),
			)
		}

		if !strings.Contains(logs.String(), "background sync failed") {
			t.Errorf("a failed sync was not logged: %q", logs.String())
		}

		cancel()

		if err := <-done; err != nil {
			t.Errorf("Run = %v, want nil when the context ends", err)
		}
	})
}

func TestRunPushesThenPulls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		fake := &fakeSync{}
		d := syncing(fake, time.Second, &bytes.Buffer{})

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() { done <- d.Run(ctx) }()

		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()

		if fake.pushes.Load() != 2 || fake.pulls.Load() != 2 {
			t.Errorf("pushes = %d, pulls = %d, want 2 and 2", fake.pushes.Load(), fake.pulls.Load())
		}

		cancel()
		<-done
	})
}

func TestRunWithANegativeIntervalOrNoRemoteDoesNotSync(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		for name, d := range map[string]*Database{
			"negative": syncing(&fakeSync{}, -1, &bytes.Buffer{}),
			"no remote": func() *Database {
				d := New(Options{})
				d.sy = &fakeSync{}

				return d
			}(),
		} {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)

			go func() { done <- d.Run(ctx) }()

			time.Sleep(30 * time.Second)
			synctest.Wait()

			if n := d.sy.(*fakeSync).pushes.Load(); n != 0 {
				t.Errorf("%s: pushes = %d, want none", name, n)
			}

			cancel()

			if err := <-done; err != nil {
				t.Errorf("%s: Run = %v", name, err)
			}
		}
	})
}

func TestSyncIsANoOpBeforeTheDatabaseIsOpen(t *testing.T) {
	t.Parallel()

	if err := New(Options{RemoteURL: "libsql://a.turso.io", AuthToken: "t"}).Sync(t.Context()); err != nil {
		t.Errorf("Sync before open = %v, want nil", err)
	}
}

func TestCloseMakesTheFinalPushOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeSync{}
	d := syncing(fake, time.Second, &bytes.Buffer{})

	if err := d.close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := d.close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if fake.pushes.Load() != 1 {
		t.Errorf("pushes = %d, want 1 final push", fake.pushes.Load())
	}

	if err := d.Sync(t.Context()); err != nil || fake.pushes.Load() != 1 {
		t.Errorf("Sync after close = %v, pushes = %d, want a no-op", err, fake.pushes.Load())
	}
}

func TestCloseReportsAFailedFinalPush(t *testing.T) {
	t.Parallel()

	d := syncing(&fakeSync{pushErr: errors.New("offline")}, time.Second, &bytes.Buffer{})

	if err := d.close(t.Context()); err == nil || !strings.Contains(err.Error(), "final push") {
		t.Errorf("close = %v, want the final push error", err)
	}
}

func TestSecurity_A02_TursoErrorsNeverQuoteTokenOrURL(t *testing.T) {
	t.Parallel()

	const (
		token  = "tok-MARKER-9f3a"
		remote = "libsql://secret-db-MARKER.turso.io"
	)

	// An SDK that echoes the URL and the token in its errors.
	leaky := &fakeSync{pushErr: errors.New("POST " + remote + "/v2/pipeline: 401 Authorization: Bearer " + token)}

	var logs bytes.Buffer

	d := New(Options{
		RemoteURL: remote, AuthToken: token, SyncInterval: time.Second,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	d.sy = leaky

	errs := []error{d.Sync(t.Context()), d.close(t.Context())}

	d.sy = leaky
	errs = append(errs, d.Sync(t.Context()))

	for i, err := range errs {
		if err == nil {
			t.Errorf("error %d is nil", i)
			continue
		}

		if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "secret-db-MARKER") {
			t.Errorf("error %d quotes the token or the URL: %v", i, err)
		}
	}

	synctest.Test(t, func(t *testing.T) {
		d.sy = leaky

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = d.Run(ctx) }()

		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		cancel()
	})

	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), "secret-db-MARKER") {
		t.Errorf("a log line quotes the token or the URL: %s", logs.String())
	}

	// Validation and URL errors do not echo their input either.
	for _, o := range []Options{{RemoteURL: "http://" + token + ".example.com", AuthToken: token}, {RemoteURL: remote}} {
		if err := o.validate(); err == nil || strings.Contains(err.Error(), token) ||
			strings.Contains(err.Error(), "MARKER") {
			t.Errorf("validate(%+v) = %v", o, err)
		}
	}

	if _, err := parseURL("turso://x?" + token); err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("parseURL error = %v", err)
	}
}

// fakeHandle is the SDK handle at open time: a syncer that connects to a
// local file.
type fakeHandle struct {
	*fakeSync

	connect func(context.Context) (*sql.DB, error)
}

func (h fakeHandle) Connect(ctx context.Context) (*sql.DB, error) { return h.connect(ctx) }

func openWith(t *testing.T, d *Database, h syncHandle) (*sql.DB, error) {
	t.Helper()

	d.newSync = func(context.Context, string) (syncHandle, error) { return h, nil }

	return d.open(t.Context(), "turso://"+filepath.ToSlash(filepath.Join(t.TempDir(), "a.db")))
}

func localConnect(t *testing.T) func(context.Context) (*sql.DB, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "local.db")

	return func(context.Context) (*sql.DB, error) { return sql.Open("turso", path) }
}

func TestOpenClearsTheSyncerWhenItFails(t *testing.T) {
	t.Parallel()

	d := New(Options{RemoteURL: "libsql://a.turso.io", AuthToken: "t"})
	h := fakeHandle{
		fakeSync: &fakeSync{},
		connect:  func(context.Context) (*sql.DB, error) { return nil, errors.New("no") },
	}

	if _, err := openWith(t, d, h); err == nil {
		t.Fatal("open = nil error")
	}

	if d.syncer() != nil {
		t.Error("a failed open left the sync handle installed: Sync and close would use a database that never opened")
	}
}

func TestFirstPullFailureIsToleratedOnlyAfterAPastSync(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		lastPull int64
		wantErr  bool
	}{
		{"never synced: fail closed", 0, true},
		{"synced before: work offline", 1700000000, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := New(Options{
				RemoteURL: "libsql://a.turso.io", AuthToken: "t", Logger: slog.New(slog.DiscardHandler),
			})
			h := fakeHandle{
				fakeSync: &fakeSync{pullErr: errors.New("offline"), lastPull: tt.lastPull},
				connect:  localConnect(t),
			}

			conn, err := openWith(t, d, h)
			if conn != nil {
				defer conn.Close()
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("open = %v, want error: %v", err, tt.wantErr)
			}

			if tt.wantErr && d.syncer() != nil {
				t.Error("the sync handle stayed installed after a failed open")
			}
		})
	}
}

func TestCloseWaitsForAnInFlightSync(t *testing.T) {
	t.Parallel()

	fake := &fakeSync{block: make(chan struct{})}
	d := syncing(fake, time.Second, &bytes.Buffer{})

	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = d.Sync(context.Background())
	}()

	for fake.pushes.Load() == 0 { // the Sync is inside its Push
		runtime.Gosched()
	}

	closed := make(chan error, 1)

	go func() { closed <- d.close(context.Background()) }()

	select {
	case <-closed:
		t.Fatal("close returned while a Sync was still pushing")
	case <-time.After(50 * time.Millisecond):
	}

	close(fake.block)
	<-done

	if err := <-closed; err != nil {
		t.Errorf("close = %v", err)
	}
}

func TestTxHoldsTheOnlyConnection(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("loads the native Turso library")
	}

	db, err := database.Open(t.Context(), "turso://"+filepath.ToSlash(filepath.Join(t.TempDir(), "a.db")),
		database.WithDriver(New(Options{}).Driver()))
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	err = db.Tx(t.Context(), func(tx *database.Tx) error {
		// The pool has one connection and the transaction holds it: a query
		// outside the transaction waits until its context ends.
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		if _, qerr := db.ExecContext(ctx, "SELECT 1"); !errors.Is(qerr, context.DeadlineExceeded) {
			t.Errorf("a query outside the Tx = %v, want it to wait until the deadline", qerr)
		}

		// The transaction's own executor works.
		_, qerr := tx.ExecContext(t.Context(), "SELECT 1")

		return qerr
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRedactHidesHostSchemesTokenAndPanics(t *testing.T) {
	t.Parallel()

	const (
		token = "tok-MARKER-77"
		host  = "secret-db-MARKER.turso.io"
	)

	d := New(Options{RemoteURL: "libsql://" + host, AuthToken: token})

	for _, raw := range []string{
		"dial tcp " + host + ":443: refused",
		"POST https://" + host + "/v2/pipeline",
		"GET http://" + host + "/x",
		"libsql://" + host,
		"header Authorization: Bearer " + token + " rejected",
		"prefix" + token + "suffix",
	} {
		got := d.redact(errors.New(raw)).Error()
		if strings.Contains(got, token) || strings.Contains(got, "MARKER") {
			t.Errorf("redact(%q) = %q", raw, got)
		}
	}

	// A panic inside the SDK may carry the token too.
	d.sy = panicSync{fakeSync: &fakeSync{}, value: "boom with " + token + " at " + host}

	err := d.Sync(t.Context())
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "MARKER") {
		t.Errorf("Sync after a panic = %v, want an error without the token or host", err)
	}
}

type panicSync struct {
	*fakeSync

	value string
}

func (p panicSync) Push(context.Context) error { panic(p.value) }

type customError struct{ secret string }

func (e customError) Error() string { return "custom " + e.secret }

func TestRedactedErrorKeepsOnlyTheSentinels(t *testing.T) {
	t.Parallel()

	d := New(Options{RemoteURL: "libsql://a.turso.io", AuthToken: "tok"})

	wrapped := d.redact(fmt.Errorf("push: %w tok", tursogo.ErrTursoConstraint))
	if !errors.Is(wrapped, tursogo.ErrTursoConstraint) {
		t.Error("the constraint sentinel was lost")
	}

	if !errors.Is(d.redact(fmt.Errorf("x tok: %w", context.DeadlineExceeded)), context.DeadlineExceeded) {
		t.Error("the deadline error was lost")
	}

	original := customError{secret: "tok"}

	hidden := d.redact(fmt.Errorf("x: %w", original))

	var ce customError
	if errors.As(hidden, &ce) || errors.Is(hidden, original) {
		t.Error("the original error is reachable through the redacted one")
	}

	if !isUniqueViolation(d.redact(fmt.Errorf("UNIQUE constraint failed: t.id tok: %w", tursogo.ErrTursoConstraint))) {
		t.Error("isUniqueViolation no longer recognises a redacted constraint error")
	}
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"http://localhost:8080":       true,
		"http://LOCALHOST:8080":       true,
		"http://127.0.0.1:8080":       true,
		"http://127.0.0.2:8080":       true,
		"http://[::1]:8080":           true,
		"http://localhost@evil.com":   false,
		"http://localhost.evil.com":   false,
		"http://evil.com/localhost":   false,
		"http://10.0.0.1":             false,
		"http://user:pw@127.0.0.1:80": true,
	}

	for raw, want := range tests {
		err := Options{RemoteURL: raw, AuthToken: "t"}.validate()
		if (err == nil) != want {
			t.Errorf("validate(%q) = %v, want accepted: %v", raw, err, want)
		}
	}
}
