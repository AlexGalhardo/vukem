package broadcast

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/sse"
	"github.com/AlexGalhardo/vukem/web"
)

// SSE is the server-sent events transport, a route handler for
// GET /broadcast/sse?channels=posts,private-user.42. Every channel is
// authorized before the stream starts; any refusal answers 400 or 403
// and nothing is streamed. Each event is sent with its name as the SSE
// event type and the Message as JSON data; presence channels first get an
// EventHere event listing the members.
//
//	r.Get("/broadcast/sse", b.SSE)
func (s *Server) SSE(c *web.Ctx) error { return s.serveSSE(c.Writer(), c.Request()) }

// SSEHandler is SSE for plain net/http muxes; refusals are written with
// http.Error.
func (s *Server) SSEHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.serveSSE(w, r); err != nil {
			if he, ok := errors.AsType[*errs.HTTPError](err); ok {
				http.Error(w, he.Text(), he.Status)
			}
		}
	})
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return &errs.HTTPError{Status: http.StatusMethodNotAllowed, Headers: http.Header{"Allow": {http.MethodGet}}}
	}

	sub, err := s.Subscribe(r.Context(), strings.Split(r.URL.Query().Get("channels"), ",")...)
	if err != nil {
		web.SecurityEvent(r, "broadcast.denied", "transport", "sse", "status", errs.Status(err))
		return err
	}
	defer sub.Close()

	return sse.Serve(w, r, func(st *sse.Stream) error {
		pump(st, sub)
		return nil
	})
}

// pump streams the subscription until the client goes away (a failed
// send), the server shuts down or the subscription ends; the browser then
// reconnects.
func pump(st *sse.Stream, sub *Subscription) {
	for _, ch := range sub.Channels() {
		if members, ok := sub.Members(ch); ok && !sendSSE(st, ch, EventHere, members) {
			return
		}
	}

	for {
		select {
		case <-st.Context().Done():
			return
		case m, ok := <-sub.Messages():
			if !ok || st.SendJSON(m.Event, m) != nil {
				return
			}
		}
	}
}

func sendSSE(st *sse.Stream, channel, event string, data any) bool {
	b, err := json.Marshal(data)

	return err == nil && st.SendJSON(event, Message{Channel: channel, Event: event, Data: b}) == nil
}
