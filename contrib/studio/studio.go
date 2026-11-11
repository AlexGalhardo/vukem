package studio

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

// Config configures a Studio.
type Config struct {
	// Sources are the data sources shown, in order.
	Sources []Source
	// Version is shown in the header and by --version.
	Version string
	// Log receives access and error logs; nil discards them.
	Log *slog.Logger
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
}

// Studio is the read-only data browser: an http.Handler guarded by the
// loopback check, the session token and the security headers.
type Studio struct {
	cfg       Config
	token     string // in the start URL, traded for the cookie
	sessionID string // the cookie value
	mux       *http.ServeMux
	pages     map[string]*template.Template
}

// New validates cfg and returns a Studio with a fresh session token.
func New(cfg Config) (*Studio, error) {
	if len(cfg.Sources) == 0 {
		return nil, errors.New("studio: at least one source is required")
	}

	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	token, err := randomToken()
	if err != nil {
		return nil, err
	}

	sessionID, err := randomToken()
	if err != nil {
		return nil, err
	}

	pages, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	s := &Studio{cfg: cfg, token: token, sessionID: sessionID, mux: http.NewServeMux(), pages: pages}
	s.routes()

	return s, nil
}

// randomToken is 32 random bytes, base64url without padding (43 characters).
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Token returns the session token the start URL carries.
func (s *Studio) Token() string { return s.token }

// Handler returns the HTTP handler: headers, access check, cross-origin
// protection, session, routes. Every route is registered through it.
func (s *Studio) Handler() http.Handler { return s.secure(s.mux) }

// Close closes every source.
func (s *Studio) Close() error {
	var errs []error
	for _, src := range s.cfg.Sources {
		errs = append(errs, src.Close())
	}

	return errors.Join(errs...)
}

func (s *Studio) routes() {
	s.mux.HandleFunc("GET /{$}", s.overview)
	s.mux.Handle("GET /assets/{file...}", s.assets())
}

func (s *Studio) overview(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "overview", nil)
}
