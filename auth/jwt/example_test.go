package jwt_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

func Example() {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := jwt.EdDSA("2026-10", priv)

	tok, _ := jwt.Sign(key, jwt.Claims{
		Subject: "42", Issuer: "https://api.example.com", Audience: []string{"api"},
		ExpiresAt: time.Now().Add(15 * time.Minute),
	})

	v := jwt.NewVerifier(jwt.StaticKeys(key), jwt.VerifyOptions{Issuer: "https://api.example.com", Audience: "api"})

	claims, err := v.Verify(context.Background(), tok)
	fmt.Println(claims.Subject, err)
	// Output: 42 <nil>
}
