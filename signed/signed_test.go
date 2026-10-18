package signed

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

func req(t *testing.T, target string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

	return r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)}))
}

func TestSignAndVerify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s := New([]byte("0123456789abcdef0123456789abcdef"))
	s.now = func() time.Time { return now }

	link, err := s.Sign("https://app.test/email/verify/42?hash=abc", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(link)
	if u.Query().Get("expires") != "1792155600" || u.Query().Get("signature") == "" || u.Host != "app.test" {
		t.Fatalf("link = %s", link)
	}

	// Host-independent: the path and query are what count.
	if err := s.Verify(req(t, u.RequestURI())); err != nil {
		t.Fatalf("Verify = %v", err)
	}

	now = now.Add(2 * time.Hour)
	if err := s.Verify(req(t, u.RequestURI())); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired = %v", err)
	}

	forever, _ := s.Sign("/unsubscribe/7", 0)
	if err := s.Verify(req(t, forever)); err != nil || strings.Contains(forever, "expires") {
		t.Fatalf("no-expiry link = %s %v", forever, err)
	}

	if _, err := s.Sign("://bad", time.Hour); err == nil {
		t.Fatal("bad URL signed")
	}
}

func TestSecurity_A01_SignedURLTampering(t *testing.T) {
	t.Parallel()

	s := New([]byte("0123456789abcdef0123456789abcdef"))
	other := New([]byte("another key another key another k"))

	link, _ := s.Sign("/email/verify/42?hash=abc", time.Hour)
	u, _ := url.Parse(link)
	q := u.Query()

	tamper := func(fn func(q url.Values), path string) string {
		c := url.Values{}
		for k, v := range q {
			c[k] = append([]string(nil), v...)
		}

		fn(c)

		return path + "?" + c.Encode()
	}

	foreign, _ := other.Sign("/email/verify/42?hash=abc", time.Hour)

	for name, target := range map[string]string{
		"other id":      tamper(func(url.Values) {}, "/email/verify/43"),
		"changed param": tamper(func(c url.Values) { c.Set("hash", "xyz") }, u.Path),
		"later expiry":  tamper(func(c url.Values) { c.Set("expires", "9999999999") }, u.Path),
		"bad expiry":    tamper(func(c url.Values) { c.Set("expires", "soon") }, u.Path),
		"no signature":  tamper(func(c url.Values) { c.Del("signature") }, u.Path),
		"other key":     foreign,
		"extra param":   tamper(func(c url.Values) { c.Set("admin", "1") }, u.Path),
	} {
		err := s.Verify(req(t, target))
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Verify = %v, want ErrInvalid", name, err)
		}
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := s.Middleware()(ok)

	for target, want := range map[string]int{link: 204, "/email/verify/42": 403} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req(t, target))

		if rec.Code != want {
			t.Errorf("middleware %s = %d, want %d", target, rec.Code, want)
		}
	}

	if errs.Status(ErrExpired) != 403 {
		t.Fatal("expired status")
	}
}

// WithClock lets the app clock (vukemtest fake clocks) expire links, so
// tests advance time instead of sleeping; nil keeps the system clock.
func TestWithClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)
	s := New([]byte(strings.Repeat("k", MinKeyLen))).WithClock(func() time.Time { return now })

	link, err := s.Sign("https://app.test/email/verify/42", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(link, "expires="+strconv.FormatInt(now.Add(time.Hour).Unix(), 10)) {
		t.Errorf("Sign() = %q, want expires one hour after the clock", link)
	}

	if err := s.Verify(req(t, link)); err != nil {
		t.Fatalf("Verify(fresh link) = %v", err)
	}

	now = now.Add(2 * time.Hour)
	if err := s.Verify(req(t, link)); !errors.Is(err, ErrExpired) {
		t.Errorf("Verify() two hours later = %v, want ErrExpired", err)
	}

	sys := New([]byte(strings.Repeat("k", MinKeyLen))).WithClock(nil)
	if err := sys.Verify(req(t, link)); !errors.Is(err, ErrExpired) {
		t.Errorf("WithClock(nil).Verify(link expired in 2000) = %v, want ErrExpired (system clock)", err)
	}
}
