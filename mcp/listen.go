package mcp

import (
	"context"
	"maps"
	"slices"
	"time"
)

type subscription struct {
	toolsChanged     bool
	promptsChanged   bool
	resourcesChanged bool
	resources        map[string]struct{}
	ch               chan notification
}

const (
	maxSubscribedURIs = 100
	subscriptionQueue = 32
)

// NotifyToolsChanged tells every subscriptions/listen stream that opted in
// that the tool list changed.
func (s *Server) NotifyToolsChanged() {
	s.broadcast(func(sub *subscription) bool { return sub.toolsChanged }, "notifications/tools/list_changed", nil)
}

// NotifyPromptsChanged tells subscribers that the prompt list changed.
func (s *Server) NotifyPromptsChanged() {
	s.broadcast(func(sub *subscription) bool { return sub.promptsChanged }, "notifications/prompts/list_changed", nil)
}

// NotifyResourcesChanged tells subscribers that the resource list changed.
func (s *Server) NotifyResourcesChanged() {
	s.broadcast(
		func(sub *subscription) bool { return sub.resourcesChanged }, "notifications/resources/list_changed", nil,
	)
}

// NotifyResourceUpdated tells the subscribers of uri that its contents changed.
func (s *Server) NotifyResourceUpdated(uri string) {
	s.broadcast(func(sub *subscription) bool {
		_, ok := sub.resources[uri]

		return ok
	}, "notifications/resources/updated", map[string]any{"uri": uri})
}

// broadcast queues a notification for every matching subscription. A
// subscriber that cannot keep up is closed (its stream ends gracefully and
// the client reconnects) instead of blocking the caller.
func (s *Server) broadcast(match func(*subscription) bool, method string, params map[string]any) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for sub := range s.subs {
		if !match(sub) {
			continue
		}

		select {
		case sub.ch <- notification{JSONRPC: "2.0", Method: method, Params: params}:
		default:
			delete(s.subs, sub)
			close(sub.ch)
		}
	}
}

func (s *Server) listen(ctx context.Context, c *call) (any, *Error) {
	var p struct {
		Notifications struct {
			Tools     bool     `json:"toolsListChanged"`
			Prompts   bool     `json:"promptsListChanged"`
			Resources bool     `json:"resourcesListChanged"`
			URIs      []string `json:"resourceSubscriptions"`
		} `json:"notifications"`
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	if c.notify == nil || len(c.msg.ID) == 0 {
		return nil, invalidParams("subscriptions/listen needs a response stream")
	}

	if len(p.Notifications.URIs) > maxSubscribedURIs {
		return nil, invalidParams("too many resourceSubscriptions")
	}

	s.mu.RLock()
	sub := &subscription{
		toolsChanged:     p.Notifications.Tools && len(s.tools) > 0,
		promptsChanged:   p.Notifications.Prompts && len(s.prompts) > 0,
		resourcesChanged: p.Notifications.Resources && (len(s.resources) > 0 || len(s.templates) > 0),
		resources:        map[string]struct{}{},
		ch:               make(chan notification, subscriptionQueue),
	}

	for _, uri := range p.Notifications.URIs {
		if s.knownURI(uri) {
			sub.resources[uri] = struct{}{}
		}
	}
	s.mu.RUnlock()

	s.subMu.Lock()

	if len(s.subs) >= s.cfg.MaxSubscriptions {
		s.subMu.Unlock()

		return nil, &Error{Code: CodeInternal, Message: "Too many subscriptions"}
	}

	s.subs[sub] = struct{}{}
	s.subMu.Unlock()

	defer func() {
		s.subMu.Lock()

		if _, ok := s.subs[sub]; ok {
			delete(s.subs, sub)
			close(sub.ch)
		}

		s.subMu.Unlock()
	}()

	meta := map[string]any{metaSubscription: c.msg.ID}

	granted := map[string]any{}
	if sub.toolsChanged {
		granted["toolsListChanged"] = true
	}

	if sub.promptsChanged {
		granted["promptsListChanged"] = true
	}

	if sub.resourcesChanged {
		granted["resourcesListChanged"] = true
	}

	if len(sub.resources) > 0 {
		granted["resourceSubscriptions"] = slices.Sorted(mapKeys(sub.resources))
	}

	c.notify("notifications/subscriptions/acknowledged", map[string]any{"_meta": meta, "notifications": granted})

	keep := time.NewTicker(s.cfg.KeepAlive)
	defer keep.Stop()

	for {
		select {
		case n, ok := <-sub.ch:
			if !ok {
				return map[string]any{"_meta": meta}, nil
			}

			params, _ := n.Params.(map[string]any)

			out := map[string]any{"_meta": meta}
			maps.Copy(out, params)

			c.notify(n.Method, out)
		case <-keep.C:
			if c.keepAlive != nil {
				c.keepAlive()
			}
		case <-s.done:
			return map[string]any{"_meta": meta}, nil
		case <-ctx.Done():
			return nil, &Error{Code: CodeInternal, Message: "Cancelled"}
		}
	}
}

func (s *Server) knownURI(uri string) bool {
	if _, ok := s.resources[uri]; ok {
		return true
	}

	for _, t := range s.templates {
		if _, ok := t.match(uri); ok {
			return true
		}
	}

	return false
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
