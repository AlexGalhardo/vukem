package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// DefaultMaintenanceFile is where `vukem down` writes the maintenance state.
const DefaultMaintenanceFile = "storage/framework/down"

// MaintenanceCookie carries the bypass granted by visiting /<secret>.
const MaintenanceCookie = "vukem_maintenance"

// MaintenanceState is the state written by `vukem down`.
type MaintenanceState struct {
	// Secret, when set, lets visitors of /<secret> through (stored hashed).
	Secret string `json:"-"`
	// Retry is sent as Retry-After.
	Retry time.Duration `json:"-"`

	SecretHash string    `json:"secret_hash,omitempty"`
	RetryAfter int       `json:"retry_after,omitempty"`
	Since      time.Time `json:"since"`
}

// WriteMaintenance puts the app in maintenance mode (vukem down).
func WriteMaintenance(file string, m MaintenanceState) error {
	if m.Secret != "" {
		sum := sha256.Sum256([]byte(m.Secret))
		m.SecretHash = hex.EncodeToString(sum[:])
	}

	m.RetryAfter = int(m.Retry.Seconds())
	if m.Since.IsZero() {
		m.Since = time.Now().UTC()
	}

	b, err := json.Marshal(m)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}

	return os.WriteFile(file, b, 0o600)
}

// MaintenanceConfig configures Maintenance.
type MaintenanceConfig struct {
	// File is the state file (default DefaultMaintenanceFile).
	File string
	// Key signs bypass cookies (derive it from APP_KEY). Without a key the
	// secret bypass is disabled: an unkeyed HMAC would be forgeable.
	Key []byte
	// PreviousKeys still accept bypass cookies signed before an APP_KEY
	// rotation (derive them from APP_PREVIOUS_KEYS); such a cookie is
	// re-issued under Key.
	PreviousKeys [][]byte
	// Except lists paths always served (default "/up").
	Except []string
	// Secure marks the bypass cookie Secure (production, HTTPS).
	Secure bool
}

// Maintenance answers 503 (Retry-After, the "errors/503" view or
// problem+json) while the state file exists. Visiting /<secret> sets a
// signed bypass cookie. A file that cannot be read keeps the app down.
func Maintenance(cfg MaintenanceConfig) Middleware {
	if cfg.File == "" {
		cfg.File = DefaultMaintenanceFile
	}

	if cfg.Except == nil {
		cfg.Except = []string{"/up"}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, down := readMaintenance(cfg.File)
			if !down {
				next.ServeHTTP(w, r)
				return
			}

			if slices.Contains(cfg.Except, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			if m.SecretHash != "" && len(cfg.Key) > 0 && bypass(w, r, next, cfg, m.SecretHash) {
				return
			}

			err := errs.New(http.StatusServiceUnavailable, "We are doing some maintenance. Please come back soon.")
			if m.RetryAfter > 0 {
				err = err.WithHeader("Retry-After", strconv.Itoa(m.RetryAfter))
			}

			web.Fail(w, r, err)
		})
	}
}

// bypass serves the request when it carries a valid bypass cookie (current
// key first, then a previous key, re-issued under the current one) or visits
// the secret path, and reports whether it answered.
func bypass(w http.ResponseWriter, r *http.Request, next http.Handler, cfg MaintenanceConfig, secretHash string) bool {
	token := bypassToken(cfg.Key, secretHash)

	if c, err := r.Cookie(MaintenanceCookie); err == nil {
		if hmac.Equal([]byte(c.Value), []byte(token)) {
			next.ServeHTTP(w, r)
			return true
		}

		if previousBypass(cfg.PreviousKeys, secretHash, c.Value) {
			setBypassCookie(w, token, cfg.Secure)
			next.ServeHTTP(w, r)

			return true
		}
	}

	sum := sha256.Sum256([]byte(strings.TrimPrefix(r.URL.Path, "/"))) // "" for "*" targets
	if hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(secretHash)) {
		setBypassCookie(w, token, cfg.Secure)
		http.Redirect(w, r, "/", http.StatusSeeOther)

		return true
	}

	return false
}

// readMaintenance reports whether the app is down; any read or parse error
// other than a missing file means down (fail closed).
func readMaintenance(file string) (MaintenanceState, bool) {
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return MaintenanceState{}, false
	}

	var m MaintenanceState
	if err != nil || json.Unmarshal(b, &m) != nil {
		return MaintenanceState{}, true
	}

	return m, true
}

// setBypassCookie grants the bypass; Secure follows cfg.Secure (off for
// plain-HTTP development).
func setBypassCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(
		w,
		&http.Cookie{ //nolint:gosec // G124: HttpOnly and SameSite set; Secure per environment
			Name: MaintenanceCookie, Value: token, Path: "/", HttpOnly: true,
			Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600,
		},
	)
}

// previousBypass reports whether value is a bypass token signed with one of
// keys (empty keys are skipped: an unkeyed HMAC would be forgeable).
func previousBypass(keys [][]byte, secretHash, value string) bool {
	for _, k := range keys {
		if len(k) > 0 && hmac.Equal([]byte(value), []byte(bypassToken(k, secretHash))) {
			return true
		}
	}

	return false
}

func bypassToken(key []byte, secretHash string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("maintenance:" + secretHash))

	return hex.EncodeToString(mac.Sum(nil))
}
