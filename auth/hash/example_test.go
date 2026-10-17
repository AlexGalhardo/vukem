package hash_test

import (
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/auth/hash"
)

func Example() {
	// Apps use hash.Default(); Fake keeps the example fast.
	h := hash.Fake()

	stored, _ := h.Make("correct horse battery staple")
	ok, _ := h.Verify(stored, "correct horse battery staple")
	bad, _ := h.Verify(stored, "wrong")

	fmt.Println(strings.HasPrefix(stored, "$argon2id$"), ok, bad)
	// Output: true true false
}

func ExampleHasher_NeedsRehash() {
	legacy, _ := hash.Bcrypt(4).Make("secret")

	// On login, a bcrypt hash verifies and is then upgraded to argon2id.
	h := hash.Fake()
	ok, _ := h.Verify(legacy, "secret")
	fmt.Println(ok, h.NeedsRehash(legacy))
	// Output: true true
}
