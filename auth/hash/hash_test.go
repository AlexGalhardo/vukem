package hash

import (
	"errors"
	"strings"
	"testing"
)

func TestRoundTrips(t *testing.T) {
	t.Parallel()

	for name, h := range map[string]Hasher{"argon2id": Fake(), "bcrypt": Bcrypt(4)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			a, err := h.Make("correct horse")
			if err != nil {
				t.Fatal(err)
			}

			b, _ := h.Make("correct horse")
			if a == b {
				t.Fatal("hashes are not salted")
			}

			for pw, want := range map[string]bool{"correct horse": true, "Correct horse": false, "": false} {
				if ok, err := h.Verify(a, pw); err != nil || ok != want {
					t.Errorf("Verify(%q) = %v %v, want %v", pw, ok, err, want)
				}
			}

			if h.NeedsRehash(a) {
				t.Error("fresh hash needs rehash")
			}
		})
	}
}

func TestArgon2PHCFormat(t *testing.T) {
	t.Parallel()

	h, err := Fake().Make("pw")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(h, "$argon2id$v=19$m=8,t=1,p=1$") || strings.Count(h, "$") != 5 {
		t.Fatalf("hash = %q", h)
	}

	// The default hasher reads hashes made with other parameters and
	// asks for a rehash.
	if ok, err := Default().Verify(h, "pw"); !ok || err != nil {
		t.Fatalf("cross-parameter verify = %v %v", ok, err)
	}

	if !Default().NeedsRehash(h) {
		t.Fatal("weaker parameters do not need rehash")
	}

	full, err := Default().Make("pw")
	if err != nil || !strings.HasPrefix(full, "$argon2id$v=19$m=65536,t=1,p=4$") || Default().NeedsRehash(full) {
		t.Fatalf("default hash = %q %v", full, err)
	}
}

func TestBcryptMigrationAndLimits(t *testing.T) {
	t.Parallel()

	old, err := Bcrypt(4).Make("pw")
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := Verify(old, "pw"); !ok || err != nil {
		t.Fatalf("Verify bcrypt = %v %v", ok, err)
	}

	if !Fake().NeedsRehash(old) || !Bcrypt(5).NeedsRehash(old) || Bcrypt(4).NeedsRehash(old) {
		t.Fatal("NeedsRehash across algorithms/costs")
	}

	long := strings.Repeat("a", 73)
	if _, err := Bcrypt(4).Make(long); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("73-byte bcrypt = %v", err)
	}

	// bcrypt would compare only the first 72 bytes; Verify refuses longer inputs.
	h72, _ := Bcrypt(4).Make(long[:72])
	if ok, _ := Verify(h72, long); ok {
		t.Fatal("73-byte password matched its 72-byte prefix")
	}

	if !Bcrypt(4).NeedsRehash("garbage") {
		t.Fatal("garbage does not need rehash")
	}
}

func TestMalformedHashes(t *testing.T) {
	t.Parallel()

	for _, h := range []string{
		"", "plain", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdA$!!",
		"$argon2id$v=19$m=0,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$a2V5",
		"$2b$04$short",
	} {
		if ok, err := Verify(h, "pw"); ok || err == nil {
			t.Errorf("Verify(%q) = %v %v, want an error", h, ok, err)
		}

		if !Fake().NeedsRehash(h) {
			t.Errorf("NeedsRehash(%q) = false", h)
		}
	}
}
