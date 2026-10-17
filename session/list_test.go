package session

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/web"
)

// loginFrom logs in as ?u= from the given user agent and remote address.
func loginFrom(t *testing.T, h http.Handler, c *client, user, ua, addr string) string {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/as?u="+user, http.NoBody)
	r.Header.Set("User-Agent", ua)
	r.RemoteAddr = addr

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	for _, ck := range rec.Result().Cookies() {
		c.cookies[ck.Name] = ck
	}

	return rec.Body.String()
}

func TestUserSessionsListsAndRevokesByHandle(t *testing.T) {
	t.Parallel()

	for name, store := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, clk := newManager(t, Config{Store: store, RecordClient: true})
			h := appHandler(m, &bytes.Buffer{})

			laptop, phone, stranger := newClient(t, h), newClient(t, h), newClient(t, h)

			laptopID := loginFrom(t, h, laptop, "7", "Laptop/1.0", "203.0.113.77:5000")
			clk.t = clk.t.Add(10 * time.Minute)
			loginFrom(t, h, phone, "7", "Phone/2.0", "[2001:db8:abcd:12::1]:443")
			loginFrom(t, h, stranger, "8", "Other/1.0", "198.51.100.1:1")

			got, err := m.UserSessions(t.Context(), "7", laptopID)

			if store == nil {
				if !errors.Is(err, ErrListUnsupported) {
					t.Fatalf("UserSessions() on cookie sessions = %v, want ErrListUnsupported", err)
				}
				if err := m.RevokeSession(t.Context(), "7", "0123456789abcdef"); !errors.Is(err, ErrRevokeUnsupported) {
					t.Fatalf("RevokeSession() on cookie sessions = %v, want ErrRevokeUnsupported", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("UserSessions() = %v", err)
			}

			start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			want := []Info{
				{
					CreatedAt: start.Add(10 * time.Minute), LastSeenAt: start.Add(10 * time.Minute),
					ExpiresAt: start.Add(10*time.Minute + 2*time.Hour),
					UserAgent: "Phone/2.0", IP: "2001:db8:abcd::",
				},
				{
					CreatedAt: start, LastSeenAt: start, ExpiresAt: start.Add(2 * time.Hour),
					UserAgent: "Laptop/1.0", IP: "203.0.113.0", Current: true,
				},
			}

			ignoreHandle := cmp.Comparer(func(a, b time.Time) bool { return a.Equal(b) })
			if diff := cmp.Diff(want, got, ignoreHandle, cmp.FilterPath(func(p cmp.Path) bool {
				return p.Last().String() == ".Handle"
			}, cmp.Ignore())); diff != "" {
				t.Fatalf("UserSessions() (-want +got):\n%s", diff)
			}

			for _, s := range got {
				if !handleRe.MatchString(s.Handle) || strings.Contains(laptopID, strings.ToUpper(s.Handle)) {
					t.Fatalf("handle %q is not a hash prefix", s.Handle)
				}
			}

			// Another user cannot revoke the session through its handle.
			if err := m.RevokeSession(t.Context(), "8", got[0].Handle); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("RevokeSession(other user) = %v, want ErrSessionNotFound", err)
			}

			for _, bad := range []string{"", "zz", got[0].Handle[:8], strings.ToUpper(got[0].Handle), "%"} {
				if err := m.RevokeSession(t.Context(), "7", bad); !errors.Is(err, ErrSessionNotFound) {
					t.Fatalf("RevokeSession(%q) = %v, want ErrSessionNotFound", bad, err)
				}
			}

			if got := phone.do("GET", "/id", nil).Body.String(); got == "" {
				t.Fatal("phone signed out before the revocation")
			}

			if err := m.RevokeSession(t.Context(), "7", got[0].Handle); err != nil {
				t.Fatalf("RevokeSession(phone) = %v", err)
			}

			left, err := m.UserSessions(t.Context(), "7", laptopID)
			if err != nil || len(left) != 1 || !left[0].Current {
				t.Fatalf("UserSessions() after revoke = %+v, %v; want the laptop only", left, err)
			}

			if s, _ := m.UserSessions(t.Context(), "8", ""); len(s) != 1 {
				t.Fatalf("stranger sessions = %d, want 1", len(s))
			}

			if _, err := m.UserSessions(t.Context(), "", ""); err == nil {
				t.Fatal("UserSessions() without a user id = nil error")
			}
		})
	}
}

// Client details are personal data: none is recorded unless RecordClient
// is set, and then the IP is truncated and the user agent capped.
func TestSecurity_A04_SessionClientInfoOptIn(t *testing.T) {
	t.Parallel()

	for _, record := range []bool{false, true} {
		m, _ := newManager(t, Config{Store: NewMemoryStore(), RecordClient: record})
		h := appHandler(m, &bytes.Buffer{})

		id := loginFrom(t, h, newClient(t, h), "7", strings.Repeat("é", 400), "203.0.113.77:5000")

		got, err := m.UserSessions(t.Context(), "7", id)
		if err != nil || len(got) != 1 {
			t.Fatalf("UserSessions() = %+v, %v", got, err)
		}

		ua, ip := got[0].UserAgent, got[0].IP

		if !record {
			if ua != "" || ip != "" {
				t.Fatalf("RecordClient=false recorded ua %q ip %q", ua, ip)
			}

			continue
		}

		if ip != "203.0.113.0" {
			t.Errorf("recorded IP = %q, want 203.0.113.0", ip)
		}

		if len(ua) > maxUserAgent || !strings.HasPrefix(ua, "éé") || strings.ContainsRune(ua, '�') {
			t.Errorf("recorded user agent %d bytes (%q...), want <= %d valid UTF-8", len(ua), ua[:4], maxUserAgent)
		}
	}
}

func TestMaskIP(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"203.0.113.77":            "203.0.113.0",
		"::ffff:203.0.113.77":     "203.0.113.0",
		"2001:db8:abcd:12::1":     "2001:db8:abcd::",
		"not an ip":               "",
		"":                        "",
		"2001:db8:abcd:ffff::ff":  "2001:db8:abcd::",
		"10.0.0.1":                "10.0.0.0",
		"[2001:db8:abcd:12::1]":   "",
		"203.0.113.77:8080":       "",
		"2001:db8::1:2:3:4:5":     "2001:db8::",
		"0.0.0.0":                 "0.0.0.0",
		"255.255.255.255":         "255.255.255.0",
		"::1":                     "::",
		"2001:0db8:0000:0000::01": "2001:db8::",
	}

	for in, want := range tests {
		if got := maskIP(in); got != want {
			t.Errorf("maskIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// A session revoked by handle while one of its requests is in flight is
// not written back.
func TestRevokeCurrentSessionByHandle(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{Store: NewMemoryStore()})
	h := appHandler(m, &bytes.Buffer{})

	c := newClient(t, h)
	id := c.do("GET", "/as?u=7", nil).Body.String()

	list, err := m.UserSessions(t.Context(), "7", id)
	if err != nil || len(list) != 1 {
		t.Fatalf("UserSessions() = %+v, %v", list, err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /self", m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := web.SessionFrom(r.Context())
		_ = s.Get("user")
		if err := m.RevokeSession(r.Context(), "7", list[0].Handle); err != nil {
			t.Error(err)
		}
		s.Put("user", "stale")
		_, _ = w.Write([]byte("ok"))
	})))

	self := newClient(t, mux)
	self.cookies = c.cookies
	self.do("GET", "/self", nil)

	if left, _ := m.UserSessions(t.Context(), "7", ""); len(left) != 0 {
		t.Fatalf("sessions after revoking the current one = %+v, want none", left)
	}
}

// Control and format runes (escape sequences, bidi overrides) are dropped
// from the recorded user agent, so a crafted header cannot spoof the
// session list or a terminal that prints it.
func TestRecordClientStripsControlRunes(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{Store: NewMemoryStore(), RecordClient: true})
	h := appHandler(m, &bytes.Buffer{})

	id := loginFrom(
		t,
		h,
		newClient(t, h),
		"7",
		"Evil\x1b[31m"+string(rune(0x202e))+"Agent\t/1.0\x00",
		"203.0.113.77:5000",
	)

	got, err := m.UserSessions(t.Context(), "7", id)
	if err != nil || len(got) != 1 {
		t.Fatalf("UserSessions() = %+v, %v", got, err)
	}

	if want := "Evil[31mAgent/1.0"; got[0].UserAgent != want {
		t.Fatalf("recorded user agent = %q, want %q", got[0].UserAgent, want)
	}
}
