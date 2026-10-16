package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/AlexGalhardo/vukem/broadcast"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Broadcast protocol message types. Clients send subscribe and
// unsubscribe; the server sends the others.
const (
	TypeSubscribe    = "subscribe"
	TypeUnsubscribe  = "unsubscribe"
	TypeSubscribed   = "subscribed"
	TypeUnsubscribed = "unsubscribed"
	TypeEvent        = "event"
	TypeHere         = "here"
	TypeJoining      = "joining"
	TypeLeaving      = "leaving"
	TypeError        = "error"
)

// Request is a client message of the broadcast protocol:
// {"type":"subscribe","channel":"private-user.42"}.
type Request struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

// Frame is a server message of the broadcast protocol. Event frames carry
// Event and Data; here carries the member list, joining/leaving one
// member; error carries Status (400, 403) and Message.
type Frame struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Event   string          `json:"event,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Status  int             `json:"status,omitempty"`
	Message string          `json:"message,omitempty"`
}

// Broadcast returns a route handler serving the broadcast WebSocket
// protocol for srv: every subscribe is authorized by srv (fail closed) in
// the context of the upgrade request, so mount it behind the auth
// middleware that identifies the user.
//
//	r.Get("/broadcast/ws", websocket.Broadcast(b))
func Broadcast(srv *broadcast.Server, opts ...Option) web.HandlerFunc {
	return func(c *web.Ctx) error {
		r := c.Request()
		return Handle(c, func(conn *Conn) error { return serveBroadcast(conn, r, srv) }, opts...)
	}
}

type channelSub struct {
	sub     *broadcast.Subscription
	stopped atomic.Bool
}

type session struct {
	conn   *Conn
	req    *http.Request
	srv    *broadcast.Server
	ctx    context.Context //nolint:containedctx // the session's lifetime, scoped to serveBroadcast
	cancel context.CancelFunc

	writeMu sync.Mutex
	wg      sync.WaitGroup
	subs    map[string]*channelSub // owned by the read loop
}

func serveBroadcast(conn *Conn, r *http.Request, srv *broadcast.Server) error {
	ctx, cancel := context.WithCancel(conn.Context())
	s := &session{conn: conn, req: r, srv: srv, ctx: ctx, cancel: cancel, subs: map[string]*channelSub{}}

	defer s.wg.Wait()
	defer s.closeAll()
	defer cancel()

	for {
		text, err := conn.ReadText(ctx)
		if errors.Is(err, ErrNotText) {
			_ = conn.Close(StatusPolicyViolation, "text frames only")
			return nil
		}

		if err != nil {
			return nil // the client left, a subscription was dropped or the app is shutting down
		}

		var req Request
		if json.Unmarshal([]byte(text), &req) != nil {
			s.send(Frame{Type: TypeError, Status: http.StatusBadRequest, Message: "Invalid message."})
			continue
		}

		switch req.Type {
		case TypeSubscribe:
			s.subscribe(req.Channel)
		case TypeUnsubscribe:
			s.unsubscribe(req.Channel)
		default:
			s.send(Frame{Type: TypeError, Status: http.StatusBadRequest, Message: "Unknown message type."})
		}
	}
}

func (s *session) send(f Frame) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.conn.WriteJSON(s.ctx, f) != nil {
		s.cancel() // a write that fails or times out ends the session
	}
}

func (s *session) fail(channel string, err error) {
	status := errs.Status(err)
	msg := http.StatusText(status)

	if he, ok := errors.AsType[*errs.HTTPError](err); ok {
		msg = he.Text()
	}

	if status == http.StatusForbidden {
		web.SecurityEvent(s.req, "broadcast.denied", "transport", "websocket", "status", status)
	}

	s.send(Frame{Type: TypeError, Channel: replyChannel(channel), Status: status, Message: msg})
}

func (s *session) subscribe(channel string) {
	if _, ok := s.subs[channel]; ok {
		s.send(Frame{Type: TypeSubscribed, Channel: channel})
		return
	}

	if len(s.subs) >= s.srv.MaxChannels() {
		s.fail(channel, broadcast.ErrTooManyChannels)
		return
	}

	sub, err := s.srv.Subscribe(s.ctx, channel)
	if err != nil {
		s.fail(channel, err)
		return
	}

	cs := &channelSub{sub: sub}
	s.subs[channel] = cs

	s.send(Frame{Type: TypeSubscribed, Channel: channel})

	if members, ok := sub.Members(channel); ok {
		if b, err := json.Marshal(members); err == nil {
			s.send(Frame{Type: TypeHere, Channel: channel, Data: b})
		}
	}

	s.wg.Go(func() { s.forward(cs) })
}

func (s *session) forward(cs *channelSub) {
	for m := range cs.sub.Messages() {
		f := Frame{Type: TypeEvent, Channel: m.Channel, Event: m.Event, Data: m.Data}

		switch m.Event {
		case broadcast.EventJoining:
			f = Frame{Type: TypeJoining, Channel: m.Channel, Data: m.Data}
		case broadcast.EventLeaving:
			f = Frame{Type: TypeLeaving, Channel: m.Channel, Data: m.Data}
		}

		s.send(f)
	}

	// A subscription that ends without unsubscribe was dropped (too slow)
	// or the server closed: end the socket so the client reconnects.
	if !cs.stopped.Load() {
		_ = s.conn.Close(StatusGoingAway, "subscription ended, reconnect")
	}
}

func (s *session) unsubscribe(channel string) {
	if cs, ok := s.subs[channel]; ok {
		cs.stopped.Store(true)
		cs.sub.Close()
		delete(s.subs, channel)
	}

	s.send(Frame{Type: TypeUnsubscribed, Channel: replyChannel(channel)})
}

// replyChannel returns channel for a reply only when it is a valid name, so
// rejected input is never reflected back.
func replyChannel(channel string) string {
	if broadcast.ValidName(channel) {
		return channel
	}

	return ""
}

func (s *session) closeAll() {
	for _, cs := range s.subs {
		cs.stopped.Store(true)
		cs.sub.Close()
	}
}
