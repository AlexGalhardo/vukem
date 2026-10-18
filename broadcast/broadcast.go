// Package broadcast pushes server events to browsers over named channels,
// in the spirit of Laravel Broadcasting: public, private ("private-") and
// presence ("presence-") channels, each authorized by a rule registered
// for its name pattern, fanned out in memory or through a Broker
// (drivers/redis) so every app instance delivers every event.
//
//	b := broadcast.New(broadcast.Config{
//		Broker:     redisClient, // nil = this process only
//		User:       func(ctx context.Context) (any, error) { return auth.UserFrom[models.User](ctx, "web") },
//		Authorizer: gate,
//	})
//	err := b.Public("posts")
//	err = b.Private("user.{id}", func(ctx context.Context, u any, p map[string]string) (bool, error) {
//		return u.(models.User).AuthID() == p["id"], nil
//	})
//	r.Get("/broadcast/sse", b.SSE) // or drivers/websocket's Broadcast handler
//
//	err = b.Broadcast(ctx, PostPublished{Post: post}) // Channels() []string
//
// Decisions fail closed: a channel without a matching rule, a rule that
// errors and a client without a user are all denied.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package broadcast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/web"
)

// Channel name prefixes that select the channel kind.
const (
	PrivatePrefix  = "private-"
	PresencePrefix = "presence-"
)

// Reserved event names for presence notifications.
const (
	EventHere    = "presence:here"
	EventJoining = "presence:joining"
	EventLeaving = "presence:leaving"
)

// Limits on names and subscriptions.
const (
	MaxNameLength      = 128
	DefaultMaxChannels = 32
)

// leaveTimeout bounds the broker publish of a presence leave after the
// client is already gone.
const leaveTimeout = 5 * time.Second

var (
	// ErrInvalidChannel rejects a malformed channel name (400).
	ErrInvalidChannel = &errs.HTTPError{Status: http.StatusBadRequest, Message: "Invalid channel name."}
	// ErrTooManyChannels rejects a subscription over Config.MaxChannels (400).
	ErrTooManyChannels = &errs.HTTPError{Status: http.StatusBadRequest, Message: "Too many channels."}
	// ErrForbidden denies a subscription (403); unauthenticated clients get
	// it too, so a probe cannot tell the two apart.
	ErrForbidden = &errs.HTTPError{Status: http.StatusForbidden, Message: "This channel is unauthorized."}
)

var errNoRule = errors.New("broadcast: no rule registered")

// Event is something broadcast on channels. The payload is BroadcastWith()
// when the event has it, the event itself otherwise, encoded as JSON; the
// event name is BroadcastAs() or the Go type name.
type Event interface {
	Channels() []string
}

type namer interface{ BroadcastAs() string }

type payloader interface{ BroadcastWith() any }

// Message is one event delivered to a subscriber.
type Message struct {
	Channel string          `json:"channel"`
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
}

// Member is a user present on a presence channel. ID identifies the user
// (several connections of one user count once); Info is shown to the
// other members.
type Member struct {
	ID   string `json:"id"`
	Info any    `json:"info,omitempty"`
}

// Config configures a Server.
type Config struct {
	// Broker carries events between instances; nil works in one process.
	Broker sse.Broker
	// User resolves the current user from a request context; nil or an
	// error denies every private and presence channel.
	User func(ctx context.Context) (any, error)
	// Authorizer backs PrivateGate rules (authz.Gate implements it).
	Authorizer web.Authorizer
	// MaxChannels caps the channels of one connection (0 = 32).
	MaxChannels int
}

// Server authorizes subscriptions and fans broadcast events out.
type Server struct {
	cfg      Config
	hub      *sse.Hub
	presence *presence

	mu    sync.RWMutex
	rules map[kind][]rule
}

// New returns a server; Close releases its hub.
func New(cfg Config) *Server {
	if cfg.MaxChannels <= 0 {
		cfg.MaxChannels = DefaultMaxChannels
	}

	return &Server{
		cfg:      cfg,
		hub:      sse.NewHub(cfg.Broker),
		presence: newPresence(),
		rules:    map[kind][]rule{},
	}
}

// Close ends every subscription.
func (s *Server) Close() { s.hub.Close() }

// MaxChannels is the per-connection channel limit transports enforce.
func (s *Server) MaxChannels() int { return s.cfg.MaxChannels }

type kind int

const (
	kindPublic kind = iota
	kindPrivate
	kindPresence
)

func kindOf(channel string) (kind, string) {
	switch {
	case strings.HasPrefix(channel, PrivatePrefix):
		return kindPrivate, strings.TrimPrefix(channel, PrivatePrefix)
	case strings.HasPrefix(channel, PresencePrefix):
		return kindPresence, strings.TrimPrefix(channel, PresencePrefix)
	default:
		return kindPublic, channel
	}
}

// ValidName reports whether name is a valid channel or event name: 1 to
// 128 characters from A-Z a-z 0-9 and _ - = @ . : (no commas, spaces or
// control characters, so lists and wire formats cannot be forged).
func ValidName(name string) bool {
	if name == "" || len(name) > MaxNameLength {
		return false
	}

	for _, r := range name {
		if !validRune(r) {
			return false
		}
	}

	return true
}

func validRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune("_-=@.:", r)
	}
}

func validChannel(channel string) bool {
	_, rest := kindOf(channel)
	return ValidName(channel) && rest != ""
}

type authorizeFunc func(ctx context.Context, user any, params map[string]string) (Member, bool, error)

type rule struct {
	segs      []string // literal segments, or "{name}" parameters
	needsUser bool
	fn        authorizeFunc
}

func parsePattern(pattern string) ([]string, error) {
	if pattern == "" || len(pattern) > MaxNameLength {
		return nil, fmt.Errorf("broadcast: invalid channel pattern %q", pattern)
	}

	if k, _ := kindOf(pattern); k != kindPublic {
		return nil, fmt.Errorf("broadcast: pattern %q must omit the private-/presence- prefix", pattern)
	}

	segs := strings.Split(pattern, ".")
	seen := map[string]bool{}

	for _, seg := range segs {
		if name, ok := paramName(seg); ok {
			if name == "" || !ValidName(name) || seen[name] {
				return nil, fmt.Errorf("broadcast: invalid parameter %q in pattern %q", seg, pattern)
			}

			seen[name] = true

			continue
		}

		if seg == "" || !ValidName(seg) {
			return nil, fmt.Errorf("broadcast: invalid segment %q in pattern %q", seg, pattern)
		}
	}

	return segs, nil
}

func paramName(seg string) (string, bool) {
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
		return seg[1 : len(seg)-1], true
	}

	return "", false
}

// match reports whether name fits segs; a parameter matches one non-empty
// segment.
func match(segs []string, name string) (map[string]string, bool) {
	parts := strings.Split(name, ".")
	if len(parts) != len(segs) {
		return nil, false
	}

	params := map[string]string{}

	for i, seg := range segs {
		if p, ok := paramName(seg); ok {
			if parts[i] == "" {
				return nil, false
			}

			params[p] = parts[i]

			continue
		}

		if seg != parts[i] {
			return nil, false
		}
	}

	return params, true
}

func (s *Server) add(k kind, pattern string, r rule) error {
	segs, err := parsePattern(pattern)
	if err != nil {
		return err
	}

	r.segs = segs

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rules[k] = append(s.rules[k], r)

	return nil
}

// Public declares public channels matching pattern ("posts",
// "posts.{id}"): anyone may subscribe. Undeclared public channels are
// denied, so a typo or a per-user name never becomes readable by default.
func (s *Server) Public(pattern string) error {
	return s.add(kindPublic, pattern, rule{fn: func(context.Context, any, map[string]string) (Member, bool, error) {
		return Member{}, true, nil
	}})
}

// Private authorizes "private-" channels matching pattern (given without
// the prefix, e.g. "user.{id}") with fn, called with the current user and
// the pattern parameters.
func (s *Server) Private(
	pattern string,
	fn func(ctx context.Context, user any, params map[string]string) (bool, error),
) error {
	if fn == nil {
		return errors.New("broadcast: nil rule")
	}

	return s.add(kindPrivate, pattern, rule{
		needsUser: true,
		fn: func(ctx context.Context, u any, p map[string]string) (Member, bool, error) {
			ok, err := fn(ctx, u, p)
			return Member{}, ok, err
		},
	})
}

// PrivateGate authorizes "private-" channels matching pattern through
// Config.Authorizer: ability is checked on the subject load returns for
// the parameters (load nil checks a gate ability with no subject).
func (s *Server) PrivateGate(
	pattern, ability string,
	load func(ctx context.Context, params map[string]string) (any, error),
) error {
	if ability == "" {
		return errors.New("broadcast: empty gate ability")
	}

	return s.add(kindPrivate, pattern, rule{
		fn: func(ctx context.Context, _ any, p map[string]string) (Member, bool, error) {
			if s.cfg.Authorizer == nil {
				return Member{}, false, errors.New("broadcast: PrivateGate needs Config.Authorizer")
			}

			var subject any

			if load != nil {
				v, err := load(ctx, p)
				if err != nil {
					return Member{}, false, fmt.Errorf("broadcast: load subject: %w", err)
				}

				subject = v
			}

			if err := s.cfg.Authorizer.Authorize(ctx, ability, subject); err != nil {
				return Member{}, false, err
			}

			return Member{}, true, nil
		},
	})
}

// Presence authorizes "presence-" channels matching pattern with fn, which
// returns the member the other subscribers see (its ID must not be empty).
func (s *Server) Presence(
	pattern string,
	fn func(ctx context.Context, user any, params map[string]string) (Member, bool, error),
) error {
	if fn == nil {
		return errors.New("broadcast: nil rule")
	}

	return s.add(kindPresence, pattern, rule{needsUser: true, fn: fn})
}

// Authorize decides whether the client of ctx may subscribe to channel
// and returns its presence member. Errors are ErrInvalidChannel or
// ErrForbidden, with the cause wrapped for logs.
func (s *Server) Authorize(ctx context.Context, channel string) (Member, error) {
	if !validChannel(channel) {
		return Member{}, ErrInvalidChannel
	}

	k, name := kindOf(channel)

	s.mu.RLock()
	rules := s.rules[k]
	s.mu.RUnlock()

	for _, r := range rules {
		params, ok := match(r.segs, name)
		if !ok {
			continue
		}

		return s.decide(ctx, k, r, params)
	}

	return Member{}, deny(fmt.Errorf("%w: channel %q", errNoRule, channel))
}

func (s *Server) decide(ctx context.Context, k kind, r rule, params map[string]string) (Member, error) {
	var user any

	if r.needsUser {
		if s.cfg.User == nil {
			return Member{}, deny(errors.New("broadcast: Config.User is not set"))
		}

		u, err := s.cfg.User(ctx)
		if err != nil || isNil(u) {
			return Member{}, deny(fmt.Errorf("broadcast: no user: %w", err))
		}

		user = u
	}

	m, ok, err := r.fn(ctx, user, params)

	switch {
	case err != nil:
		return Member{}, deny(err)
	case !ok:
		return Member{}, ErrForbidden
	case k == kindPresence && m.ID == "":
		return Member{}, deny(errors.New("broadcast: presence rule returned a member without ID"))
	}

	return m, nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func deny(cause error) error {
	return &errs.HTTPError{Status: ErrForbidden.Status, Message: ErrForbidden.Message, Err: cause}
}

func topic(channel string) string { return "broadcast:" + channel }

// envelope is what travels through the hub; Origin lets a subscription
// skip its own presence notifications.
type envelope struct {
	Channel string          `json:"channel"`
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
	Origin  string          `json:"origin,omitempty"`
}

// Broadcast publishes event on each of its channels.
func (s *Server) Broadcast(ctx context.Context, event Event) error {
	if event == nil {
		return errors.New("broadcast: nil event")
	}

	name := eventName(event)

	var payload any = event
	if p, ok := event.(payloader); ok {
		payload = p.BroadcastWith()
	}

	channels := event.Channels()
	if len(channels) == 0 {
		return fmt.Errorf("broadcast: %s has no channels", name)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("broadcast: encode %s: %w", name, err)
	}

	var errList []error

	for _, ch := range channels {
		errList = append(errList, s.publish(ctx, ch, name, data, ""))
	}

	return errors.Join(errList...)
}

// Publish sends one event named event with data (encoded as JSON) on
// channel, for callers without an Event type.
func (s *Server) Publish(ctx context.Context, channel, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("broadcast: encode %s: %w", event, err)
	}

	return s.publish(ctx, channel, event, b, "")
}

func (s *Server) publish(ctx context.Context, channel, event string, data []byte, origin string) error {
	if !validChannel(channel) {
		return fmt.Errorf("broadcast: invalid channel %q", channel)
	}

	if !ValidName(event) || (origin == "" && strings.HasPrefix(event, "presence:")) {
		return fmt.Errorf("broadcast: invalid or reserved event name %q", event)
	}

	b, err := json.Marshal(envelope{Channel: channel, Event: event, Data: data, Origin: origin})
	if err != nil {
		return fmt.Errorf("broadcast: encode envelope: %w", err)
	}

	if err := s.hub.Publish(ctx, topic(channel), sse.Event{Event: event, Data: string(b)}); err != nil {
		return fmt.Errorf("broadcast: publish %s on %s: %w", event, channel, err)
	}

	return nil
}

func eventName(event Event) string {
	if n, ok := event.(namer); ok {
		return n.BroadcastAs()
	}

	t := reflect.TypeOf(event)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t.Name()
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails

	return hex.EncodeToString(b)
}
