package schema

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type dataKey struct{}

func TestDeferredMigrationWaitsForRunDeferred(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)

	var sealedWith []string

	seal := mig("20260201_seal_notes", func(s *Schema) error {
		if s.Recording() {
			return s.Raw("UPDATE notes SET sealed = 1")
		}

		key, ok := s.Context().Value(dataKey{}).(string)
		if !ok {
			return errors.New("no data key in the context")
		}

		sealedWith = append(sealedWith, key)

		return s.Raw("UPDATE notes SET sealed = 1")
	})
	seal.Deferred = true
	seal.Down = func(s *Schema) error { return s.Raw("UPDATE notes SET sealed = 0") }

	notes := mig("20260101_notes", func(s *Schema) error {
		return s.Create("notes", func(t *Table) {
			t.ID()
			t.Boolean("sealed").Default(false)
		})
	})

	m, err := NewMigrator(db, []Migration{notes, seal, mig("20260301_tags", createTable("tags"))})
	if err != nil {
		t.Fatal(err)
	}

	ran, err := m.Up(t.Context())
	if err != nil || !cmp.Equal(ran, []string{"20260101_notes", "20260301_tags"}) {
		t.Fatalf("Up = %v, %v; want notes and tags, the deferred seal left pending", ran, err)
	}

	st, err := m.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	wantStatus := []Status{
		{ID: "20260101_notes", Ran: true, Batch: 1},
		{ID: "20260201_seal_notes", Deferred: true},
		{ID: "20260301_tags", Ran: true, Batch: 1},
	}
	if diff := cmp.Diff(wantStatus, st, cmpopts.IgnoreFields(Status{}, "RanAt")); diff != "" {
		t.Errorf("Status (-want +got):\n%s", diff)
	}

	if ran, err := m.Up(t.Context()); err != nil || len(ran) != 0 {
		t.Errorf("second Up = %v, %v; want nothing, the deferred one still waiting", ran, err)
	}

	ctx := context.WithValue(t.Context(), dataKey{}, "k1")

	ran, err = m.RunDeferred(ctx)
	if err != nil || !cmp.Equal(ran, []string{"20260201_seal_notes"}) {
		t.Fatalf("RunDeferred = %v, %v; want the seal", ran, err)
	}

	if !cmp.Equal(sealedWith, []string{"k1"}) {
		t.Errorf("the migration ran with keys %v, want [k1] once", sealedWith)
	}

	if ran, err := m.RunDeferred(ctx); err != nil || len(ran) != 0 {
		t.Errorf("second RunDeferred = %v, %v; want nothing", ran, err)
	}

	st, err = m.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if s := st[1]; !s.Ran || s.Deferred || s.Batch != 2 || s.Changed {
		t.Errorf("Status of the seal after RunDeferred = %+v, want ran in batch 2", s)
	}

	if ran, err := m.Rollback(t.Context(), 1); err != nil || !cmp.Equal(ran, []string{"20260201_seal_notes"}) {
		t.Errorf("Rollback = %v, %v; want the deferred batch undone", ran, err)
	}
}

func TestRunDeferredNamesOnlyDeferredMigrations(t *testing.T) {
	t.Parallel()

	a := mig("20260101_a", createTable("a"))
	b := mig("20260201_b", createTable("b"))
	b.Deferred = true
	c := mig("20260301_c", createTable("c"))
	c.Deferred = true

	m, err := NewMigrator(rebuildDB(t), []Migration{a, b, c})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.RunDeferred(
		t.Context(),
		"20260101_a",
	); err == nil ||
		!strings.Contains(err.Error(), "not deferred") {
		t.Errorf("RunDeferred(a) = %v, want a refusal: a is not deferred", err)
	}

	if _, err := m.RunDeferred(t.Context(), "nope"); !errors.Is(err, ErrUnknownMigration) {
		t.Errorf("RunDeferred(nope) = %v, want ErrUnknownMigration", err)
	}

	ran, err := m.RunDeferred(t.Context(), "20260301_c")
	if err != nil || !cmp.Equal(ran, []string{"20260301_c"}) {
		t.Errorf("RunDeferred(c) = %v, %v; want only c", ran, err)
	}

	// b sorts before the applied c, yet it is no out-of-order migration.
	if ran, err := m.Up(t.Context()); err != nil || !cmp.Equal(ran, []string{"20260101_a"}) {
		t.Errorf("Up = %v, %v; want only a", ran, err)
	}
}
