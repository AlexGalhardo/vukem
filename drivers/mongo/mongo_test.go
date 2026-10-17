package mongo

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenErrors(t *testing.T) {
	t.Parallel()

	if _, err := Open(t.Context(), "not-a-uri"); err == nil {
		t.Error("bad URI accepted")
	}

	if _, err := Open(t.Context(), "mongodb://localhost:1"); err == nil ||
		!strings.Contains(err.Error(), "MONGO_DATABASE") {
		t.Errorf("Open(no database) = %v, want an error naming MONGO_DATABASE", err)
	}

	// Without a server the ping fails once the context ends.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if _, err := Open(ctx, "mongodb://127.0.0.1:1/app?serverSelectionTimeoutMS=500"); err == nil {
		t.Error("Open without a server succeeded")
	}
}

func TestOpenLazy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, uri, db, want string
		wantErr             bool
	}{
		{name: "database from the path", uri: "mongodb://127.0.0.1:1/app", want: "app"},
		{name: "database argument without a path", uri: "mongodb://u:p@127.0.0.1:1", db: "shop", want: "shop"},
		{name: "argument overrides the path", uri: "mongodb://127.0.0.1:1/app", db: "shop", want: "shop"},
		{name: "empty argument falls back to the path", uri: "mongodb://127.0.0.1:1/app", db: "", want: "app"},
		{name: "no database anywhere", uri: "mongodb://127.0.0.1:1", wantErr: true},
		{name: "malformed URI", uri: "mongodb://127.0.0.1:notaport/app", wantErr: true},
		{name: "not a URI", uri: "localhost", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// No server listens on port 1: OpenLazy must not contact it.
			m, err := OpenLazy(tt.uri, tt.db)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("OpenLazy(%q, %q) = nil error, want an error", tt.uri, tt.db)
				}

				return
			}

			if err != nil {
				t.Fatalf("OpenLazy(%q, %q) = %v", tt.uri, tt.db, err)
			}

			t.Cleanup(func() { _ = m.Close(context.WithoutCancel(t.Context())) })

			if got := m.Database().Name(); got != tt.want {
				t.Errorf("OpenLazy(%q, %q).Database().Name() = %q, want %q", tt.uri, tt.db, got, tt.want)
			}
		})
	}
}

func TestOpenLazyPingReportsReadiness(t *testing.T) {
	t.Parallel()

	m, err := OpenLazy("mongodb://127.0.0.1:1/app?serverSelectionTimeoutMS=300")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = m.Close(context.WithoutCancel(t.Context())) })

	if err := m.Ping(t.Context()); err == nil {
		t.Error("Ping without a server = nil, want an error")
	}
}
