package crypt_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/crypt"
)

func Example() {
	// In an app, use app.Crypt (keys derived from APP_KEY and
	// APP_PREVIOUS_KEYS) instead of literal keys.
	oldKey := bytes.Repeat([]byte{1}, crypt.KeySize)
	newKey := bytes.Repeat([]byte{2}, crypt.KeySize)

	before, _ := crypt.New(oldKey)
	token := before.EncryptString("4111 1111 1111 1111")

	// After rotating APP_KEY, the old key stays as a previous key.
	after, _ := crypt.New(newKey, oldKey)
	plain, err := after.DecryptString(token)
	fmt.Println(plain, err, after.NeedsRotation(token))

	renewed, _ := after.Rotate(token, nil)
	fmt.Println(after.NeedsRotation(renewed))
	// Output:
	// 4111 1111 1111 1111 <nil> true
	// false
}

func ExampleEncrypter_Seal() {
	enc, _ := crypt.New(bytes.Repeat([]byte{7}, crypt.KeySize))

	// The additional data binds the token to its purpose.
	token := enc.Seal([]byte("42"), []byte("invoice:total"))

	_, err := enc.Open(token, []byte("invoice:discount"))
	fmt.Println(err)

	plain, _ := enc.Open(token, []byte("invoice:total"))
	fmt.Println(string(plain))
	// Output:
	// crypt: invalid or tampered ciphertext
	// 42
}

func ExampleEncrypter_SetCookie() {
	enc, _ := crypt.New(bytes.Repeat([]byte{9}, crypt.KeySize))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	_ = enc.SetCookie(w, r, http.Cookie{Name: "cart", Value: "3 items", Path: "/"})

	next := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	for _, c := range w.Result().Cookies() {
		next.AddCookie(c)
	}

	value, err := enc.Cookie(next, "cart")
	fmt.Println(value, err)
	// Output: 3 items <nil>
}
