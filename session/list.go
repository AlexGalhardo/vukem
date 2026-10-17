package session

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/web"
)

// StoredSession is one session as a ListStore returns it.
type StoredSession struct {
	// Key is hex(SHA-256(session id)), never the id itself.
	Key string
	// Data is the encoded session, as passed to Create and Update.
	Data []byte
	// Expiry is when the session ends unless it is used again.
	Expiry time.Time
}

// ListStore is a UserStore that lists the sessions of a user and deletes
// one of them by key, for an "active sessions" page. SQLStore, MemoryStore
// and drivers/redis implement it.
type ListStore interface {
	UserStore
	// ListUser returns the live sessions of userID.
	ListUser(ctx context.Context, userID string) ([]StoredSession, error)
	// DeleteUserKey deletes the session stored under key when it belongs
	// to userID and reports whether one was deleted.
	DeleteUserKey(ctx context.Context, userID, key string) (bool, error)
}

// ErrListUnsupported is returned by Manager.UserSessions when the sessions
// live in cookies or in a store that is not a ListStore.
var ErrListUnsupported = errors.New(
	"session: listing sessions needs a server-side ListStore (SESSION_DRIVER=database)",
)

// Info describes one session of a user.
type Info struct {
	// Handle names the session for RevokeSession: the first 16 hex digits
	// of SHA-256(storage key). It is safe to show and cannot load the
	// session.
	Handle string
	// CreatedAt is when the session started (the login regenerates it).
	CreatedAt time.Time
	// LastSeenAt is the last request, to the minute (writes that only
	// refresh the idle timeout happen at most once a minute).
	LastSeenAt time.Time
	// ExpiresAt is when the session ends unless it is used again.
	ExpiresAt time.Time
	// UserAgent and IP are recorded at login only with Config.RecordClient;
	// IP keeps the network part only (IPv4 /24, IPv6 /48).
	UserAgent, IP string
	// Current is true for the session of the currentID given to
	// UserSessions.
	Current bool
}

const (
	keyClientUA = "_client.ua"
	keyClientIP = "_client.ip"
	// maxUserAgent caps the recorded User-Agent in bytes.
	maxUserAgent = 256
	handleLen    = 16
)

var handleRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// handleOf derives the public handle from a storage key. It is a hash of
// the key rather than a prefix of it, so a handle shown in a page does not
// even narrow down a row of the sessions table.
func handleOf(key string) string { return storageKey("handle:" + key)[:handleLen] }

// UserSessions lists the live sessions of userID (the owner recorded under
// web.SessionUserKey, auth.SessionGuard.SessionUserID), most recently used
// first; currentID marks the caller's own session. It returns
// ErrListUnsupported for cookie sessions and stores that are not a
// ListStore.
func (m *Manager) UserSessions(ctx context.Context, userID, currentID string) ([]Info, error) {
	ls, ok := m.cfg.Store.(ListStore)
	if !ok {
		return nil, ErrListUnsupported
	}

	if userID == "" {
		return nil, errNoUser
	}

	stored, err := ls.ListUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	current := ""
	if currentID != "" {
		current = storageKey(currentID)
	}

	out := make([]Info, 0, len(stored))

	for _, st := range stored {
		info := Info{Handle: handleOf(st.Key), ExpiresAt: st.Expiry, Current: st.Key == current}

		// Undecodable data is still listed, so it can be revoked.
		if data, err := decode(st.Data); err == nil {
			info.CreatedAt, _ = unixOf(data[keyCreated])
			info.LastSeenAt, _ = unixOf(data[keyLast])
			info.UserAgent, _ = data[keyClientUA].(string)
			info.IP, _ = data[keyClientIP].(string)
		}

		out = append(out, info)
	}

	slices.SortStableFunc(out, func(a, b Info) int {
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), strings.Compare(a.Handle, b.Handle))
	})

	return out, nil
}

// RevokeSession ends the session of userID named by handle (from
// UserSessions). It returns ErrSessionNotFound when handle names no
// session of that user, so one user cannot end another's sessions, and
// ErrRevokeUnsupported for cookie sessions and stores that are not a
// ListStore.
func (m *Manager) RevokeSession(ctx context.Context, userID, handle string) error {
	ls, ok := m.cfg.Store.(ListStore)
	if !ok {
		return ErrRevokeUnsupported
	}

	if userID == "" {
		return errNoUser
	}

	if !handleRe.MatchString(handle) {
		return ErrSessionNotFound
	}

	stored, err := ls.ListUser(ctx, userID)
	if err != nil {
		return err
	}

	deleted := false

	// Two sessions of one user sharing a 64-bit handle is not expected;
	// should it happen, both end (the safe side).
	for _, st := range stored {
		if handleOf(st.Key) != handle {
			continue
		}

		ok, err := ls.DeleteUserKey(ctx, userID, st.Key)
		if err != nil {
			return err
		}

		deleted = deleted || ok
	}

	if !deleted {
		return ErrSessionNotFound
	}

	return nil
}

// recordClient stores the client's user agent and network on a session
// about to be created for a logged-in user (Config.RecordClient).
func (m *Manager) recordClient(w http.ResponseWriter, r *http.Request, s *Session) {
	if !m.cfg.RecordClient || s.id == s.storedID {
		return
	}

	if _, ok := m.cfg.Store.(ListStore); !ok {
		return
	}

	if user, _ := s.data[web.SessionUserKey].(string); user == "" {
		return
	}

	s.data[keyClientUA] = truncateUTF8(r.UserAgent(), maxUserAgent)
	s.data[keyClientIP] = maskIP(web.NewCtx(w, r, web.ServicesFrom(r.Context())).IP())
}

// maskIP keeps the network part of ip (IPv4 /24, IPv6 /48): enough to
// tell "home" from "abroad", not to single out a host.
func maskIP(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}

	addr = addr.Unmap().WithZone("")

	bits := 48
	if addr.Is4() {
		bits = 24
	}

	p, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}

	return p.Addr().String()
}

func truncateUTF8(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}

	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}

	return s
}
