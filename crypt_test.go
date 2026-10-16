package vukem

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/crypt"
)

func TestSecurity_A04_AppCryptRotatesWithPreviousKeys(t *testing.T) {
	t.Parallel()

	oldKey, newKey := config.GenerateKey(), config.GenerateKey()

	before := newTestApp(t, WithSource(testSource(map[string]string{"APP_KEY": oldKey})))
	token := before.Crypt.EncryptString("secret")

	after := newTestApp(t, WithSource(testSource(map[string]string{
		"APP_KEY":           newKey,
		"APP_PREVIOUS_KEYS": " " + config.GenerateKey() + " , " + oldKey + ",",
	})))

	if got, err := after.Crypt.DecryptString(token); err != nil || got != "secret" {
		t.Fatalf("DecryptString(old token) = %q, %v, want secret, nil", got, err)
	}

	if !after.Crypt.NeedsRotation(token) {
		t.Fatal("NeedsRotation(old token) = false, want true")
	}

	fresh := newTestApp(t, WithSource(testSource(map[string]string{"APP_KEY": newKey})))
	if _, err := fresh.Crypt.DecryptString(token); !errors.Is(err, crypt.ErrInvalid) {
		t.Fatalf("DecryptString(old token) without APP_PREVIOUS_KEYS error = %v, want crypt.ErrInvalid", err)
	}
}

func TestSecurity_A04_AppCryptUsesDerivedKey(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(testKey, "base64:"))
	if err != nil {
		t.Fatal(err)
	}

	rawEnc, err := crypt.New(raw)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rawEnc.DecryptString(app.Crypt.EncryptString("x")); !errors.Is(err, crypt.ErrInvalid) {
		t.Fatalf("raw APP_KEY opened app.Crypt token: err = %v, want crypt.ErrInvalid", err)
	}
}

func TestBootRefusesBadPreviousKeys(t *testing.T) {
	t.Parallel()

	_, err := New(
		WithSource(testSource(map[string]string{"APP_PREVIOUS_KEYS": "base64:YWJj"})),
		WithLogger(discardLogger()),
	)
	if err == nil || !strings.Contains(err.Error(), "APP_PREVIOUS_KEYS") {
		t.Fatalf("New(short previous key) error = %v, want an APP_PREVIOUS_KEYS error", err)
	}
}
