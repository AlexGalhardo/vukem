// Package sessiontest is the conformance suite every session.ListStore
// passes (memory and database in core, Redis in drivers/redis). A new
// store calls [Run] from one of its tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md.
package sessiontest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/session"
)

// Run checks store. Sessions use fresh random ids and users, and expire an
// hour after time.Now, so a shared server is fine.
func Run(t *testing.T, store session.ListStore) {
	t.Helper()

	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	user := func() string { return "u:" + rand.Text()[:10] }

	t.Run("CreateUpdateDelete", func(t *testing.T) {
		id, u := rand.Text(), user()

		if err := store.Create(ctx, id, u, []byte(`{"v":1}`), exp); err != nil {
			t.Fatalf("Create() = %v", err)
		}

		if err := store.Update(ctx, id, u, []byte(`{"v":2}`), exp); err != nil {
			t.Fatalf("Update() = %v", err)
		}

		if data, found, err := store.Load(ctx, id); err != nil || !found || string(data) != `{"v":2}` {
			t.Fatalf("Load() = %s, %v, %v; want {\"v\":2}", data, found, err)
		}

		if err := store.Delete(ctx, id); err != nil {
			t.Fatalf("Delete() = %v", err)
		}

		if _, found, err := store.Load(ctx, id); err != nil || found {
			t.Fatalf("Load() after Delete = %v, %v; want not found", found, err)
		}
	})

	t.Run("UpdateNeverResurrects", func(t *testing.T) {
		id, u := rand.Text(), user()

		if err := store.Update(ctx, id, u, []byte(`{}`), exp); !errors.Is(err, session.ErrSessionNotFound) {
			t.Fatalf("Update(unknown) = %v, want ErrSessionNotFound", err)
		}

		if _, found, _ := store.Load(ctx, id); found {
			t.Fatal("Update(unknown) created the session")
		}

		if list, _ := store.ListUser(ctx, u); len(list) != 0 {
			t.Fatalf("Update(unknown) listed %d sessions", len(list))
		}
	})

	t.Run("RevokeOthers", func(t *testing.T) {
		u, other := user(), user()
		keep, drop1, drop2, stranger := rand.Text(), rand.Text(), rand.Text(), rand.Text()

		for id, owner := range map[string]string{keep: u, drop1: u, drop2: u, stranger: other} {
			if err := store.Create(ctx, id, owner, []byte(`{}`), exp); err != nil {
				t.Fatal(err)
			}
		}

		if err := store.RevokeOthers(ctx, u, keep); err != nil {
			t.Fatalf("RevokeOthers() = %v", err)
		}

		for id, want := range map[string]bool{keep: true, drop1: false, drop2: false, stranger: true} {
			if _, found, err := store.Load(ctx, id); err != nil || found != want {
				t.Errorf("Load() after RevokeOthers = %v, %v; want found %v", found, err, want)
			}
		}

		if err := store.Update(ctx, drop1, u, []byte(`{}`), exp); !errors.Is(err, session.ErrSessionNotFound) {
			t.Errorf("Update(revoked) = %v, want ErrSessionNotFound", err)
		}

		if err := store.RevokeOthers(ctx, u, ""); err != nil {
			t.Fatal(err)
		}

		if _, found, _ := store.Load(ctx, keep); found {
			t.Error("RevokeOthers(keep \"\") kept a session")
		}

		if err := store.RevokeOthers(ctx, "", ""); err == nil {
			t.Error("RevokeOthers() without a user = nil error")
		}
	})

	t.Run("ListAndDeleteUserKey", func(t *testing.T) {
		u, other := user(), user()
		a, b, guest := rand.Text(), rand.Text(), rand.Text()

		for id, owner := range map[string]string{a: u, b: u, guest: ""} {
			if err := store.Create(ctx, id, owner, []byte(`{"id":"`+id+`"}`), exp); err != nil {
				t.Fatal(err)
			}
		}

		list, err := store.ListUser(ctx, u)
		if err != nil {
			t.Fatalf("ListUser() = %v", err)
		}

		keys := make([]string, 0, len(list))
		for _, s := range list {
			keys = append(keys, s.Key)

			if s.Expiry.Unix() != exp.Unix() || len(s.Data) == 0 {
				t.Errorf("listed %+v, want expiry %v and data", s, exp)
			}
		}

		slices.Sort(keys)

		want := []string{digest(a), digest(b)}
		slices.Sort(want)

		if !slices.Equal(keys, want) {
			t.Fatalf("ListUser() keys = %v, want %v (hex SHA-256 of the ids)", keys, want)
		}

		if ok, err := store.DeleteUserKey(ctx, other, digest(a)); err != nil || ok {
			t.Fatalf("DeleteUserKey(other user) = %v, %v; want false", ok, err)
		}

		if ok, err := store.DeleteUserKey(ctx, u, a); err != nil || ok {
			t.Fatalf("DeleteUserKey(clear id) = %v, %v; want false", ok, err)
		}

		if ok, err := store.DeleteUserKey(ctx, u, digest(a)); err != nil || !ok {
			t.Fatalf("DeleteUserKey() = %v, %v; want true", ok, err)
		}

		if _, found, _ := store.Load(ctx, a); found {
			t.Fatal("session loaded after DeleteUserKey")
		}

		// A session moving to another owner leaves the old owner's list.
		if err := store.Update(ctx, b, other, []byte(`{}`), exp); err != nil {
			t.Fatal(err)
		}

		if list, _ := store.ListUser(ctx, u); len(list) != 0 {
			t.Errorf("old owner still lists %d sessions", len(list))
		}

		if list, _ := store.ListUser(ctx, other); len(list) != 1 {
			t.Errorf("new owner lists %d sessions, want 1", len(list))
		}

		if _, err := store.ListUser(ctx, ""); err == nil {
			t.Error("ListUser() without a user = nil error")
		}
	})
}

func digest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
