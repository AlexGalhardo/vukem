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

			if m.SecretHash != "" && len(cfg.Key) > 0 {
				token := bypassToken(cfg.Key, m.SecretHash)

				if c, err := r.Cookie(MaintenanceCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(token)) {
					next.ServeHTTP(w, r)
					return
				}

				sum := sha256.Sum256([]byte(strings.TrimPrefix(r.URL.Path, "/"))) // "" for "*" targets
				if hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(m.SecretHash)) {
					// Secure follows cfg.Secure: off for plain-HTTP development.
					http.SetCookie(
						w,
						&http.Cookie{ //nolint:gosec // G124: HttpOnly and SameSite set; Secure per environment
							Name: MaintenanceCookie, Value: token, Path: "/", HttpOnly: true,
							Secure: cfg.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600,
						},
					)
					http.Redirect(w, r, "/", http.StatusSeeOther)

					return
				}
			}

			err := errs.New(http.StatusServiceUnavailable, "We are doing some maintenance. Please come back soon.")
			if m.RetryAfter > 0 {
				err = err.WithHeader("Retry-After", strconv.Itoa(m.RetryAfter))
			}

			web.Fail(w, r, err)
		})
	}
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

func bypassToken(key []byte, secretHash string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("maintenance:" + secretHash))

	return hex.EncodeToString(mac.Sum(nil))
}
