package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Handler handles one update.
type Handler func(ctx context.Context, c *Context) error

// Middleware wraps a Handler, like func(http.Handler) http.Handler.
type Middleware func(Handler) Handler

// UpdateHandler is what the webhook and the poller feed; Router implements it.
type UpdateHandler interface {
	HandleUpdate(ctx context.Context, bot *Bot, u Update) error
}

// ErrNoHandler is returned by a Router for an update no route matches.
var ErrNoHandler = errors.New("telegram: no handler for the update")

// Context is the update being handled.
type Context struct {
	Bot    *Bot
	Update Update
	// Args are the words after a command: "/pay 10 usd" gives ["10", "usd"].
	Args []string

	state *State
	dirty bool
}

// Message returns the message of the update, edited or new, or nil.
func (c *Context) Message() *Message {
	switch {
	case c.Update.Message != nil:
		return c.Update.Message
	case c.Update.EditedMessage != nil:
		return c.Update.EditedMessage
	case c.Update.CallbackQuery != nil:
		return c.Update.CallbackQuery.Message
	}

	return nil
}

// User returns the sender, or a zero User.
func (c *Context) User() User {
	switch {
	case c.Update.CallbackQuery != nil:
		return c.Update.CallbackQuery.From
	case c.Message() != nil && c.Message().From != nil:
		return *c.Message().From
	}

	return User{}
}

// ChatID returns the chat of the update, or 0.
func (c *Context) ChatID() int64 {
	if m := c.Message(); m != nil {
		return m.Chat.ID
	}

	return 0
}

// Reply sends text to the chat of the update.
func (c *Context) Reply(ctx context.Context, text string) error {
	if c.ChatID() == 0 {
		return errors.New("telegram: the update has no chat")
	}

	_, err := c.Bot.SendMessage(ctx, SendMessage{ChatID: c.ChatID(), Text: text})

	return err
}

// Answer acknowledges the callback query of the update.
func (c *Context) Answer(ctx context.Context, text string) error {
	if c.Update.CallbackQuery == nil {
		return errors.New("telegram: the update is not a callback query")
	}

	return c.Bot.AnswerCallbackQuery(ctx, c.Update.CallbackQuery.ID, text)
}

// State returns the conversation state; it is empty without the
// Conversations middleware.
func (c *Context) State() State {
	if c.state == nil {
		return State{}
	}

	return *c.state
}

// SetState replaces the conversation state; Conversations saves it after the
// handler returns.
func (c *Context) SetState(s State) {
	c.state, c.dirty = &s, true
}

// ClearState ends the conversation.
func (c *Context) ClearState() { c.SetState(State{}) }

type route struct {
	match func(c *Context) bool
	h     Handler
}

// Router sends updates to handlers: commands by name, callback queries by
// data prefix, messages by conversation step, then the fallbacks. The first
// match wins, in registration order within each kind.
type Router struct {
	mw        []Middleware
	commands  map[string]Handler
	callbacks []route
	steps     map[string]Handler
	message   Handler
	fallback  Handler
	// Username is the bot username without @; with it "/start@thisbot" is
	// ours and "/start@otherbot" is ignored. Empty accepts any suffix.
	Username string
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	return &Router{commands: map[string]Handler{}, steps: map[string]Handler{}}
}

// Use adds middleware; the first added runs outermost.
func (r *Router) Use(mw ...Middleware) { r.mw = append(r.mw, mw...) }

// Command handles "/name" (with or without @botname). It panics on a
// duplicate or invalid name, like http.ServeMux.
func (r *Router) Command(name string, h Handler) {
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	if name == "" || strings.ContainsAny(name, " @") {
		panic(fmt.Sprintf("telegram: invalid command %q", name))
	}

	if _, dup := r.commands[name]; dup {
		panic(fmt.Sprintf("telegram: command %q registered twice", name))
	}

	r.commands[name] = h
}

// Callback handles callback queries whose data starts with prefix.
func (r *Router) Callback(prefix string, h Handler) {
	r.callbacks = append(r.callbacks, route{
		match: func(c *Context) bool {
			return c.Update.CallbackQuery != nil && strings.HasPrefix(c.Update.CallbackQuery.Data, prefix)
		},
		h: h,
	})
}

// Step handles a text message while the conversation is at step; set it with
// Context.SetState. It needs the Conversations middleware.
func (r *Router) Step(step string, h Handler) {
	if step == "" {
		panic("telegram: empty step")
	}

	r.steps[step] = h
}

// Message handles a text message that is not a command and matches no step.
func (r *Router) Message(h Handler) { r.message = h }

// Fallback handles every other update; without it ErrNoHandler is returned.
func (r *Router) Fallback(h Handler) { r.fallback = h }

// HandleUpdate implements UpdateHandler. A panic in a handler is returned as
// an error and never crosses the router.
func (r *Router) HandleUpdate(ctx context.Context, bot *Bot, u Update) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("telegram: handler panic: %v", p)
		}
	}()

	c := &Context{Bot: bot, Update: u}
	h := r.pick(c)

	if h == nil {
		return ErrNoHandler
	}

	for _, mw := range slices.Backward(r.mw) {
		h = mw(h)
	}

	return h(ctx, c)
}

// pick chooses the handler; conversation state is loaded by middleware that
// wraps the whole dispatch, so Step routes resolve inside a wrapper handler.
func (r *Router) pick(c *Context) Handler {
	if cq := c.Update.CallbackQuery; cq != nil {
		for _, rt := range r.callbacks {
			if rt.match(c) {
				return rt.h
			}
		}

		return r.fallback
	}

	m := c.Update.Message
	if m == nil {
		return r.fallback
	}

	if name, args, ok := r.command(m.Text); ok {
		if h, found := r.commands[name]; found {
			c.Args = args

			return h
		}

		return r.fallback
	}

	// An unparsed slash is a command for another bot, which is not ours to
	// answer as free text.
	if m.Text == "" || strings.HasPrefix(m.Text, "/") {
		return r.fallback
	}

	return func(ctx context.Context, c *Context) error {
		if h, ok := r.steps[c.State().Step]; ok && c.State().Step != "" {
			return h(ctx, c)
		}

		switch {
		case r.message != nil:
			return r.message(ctx, c)
		case r.fallback != nil:
			return r.fallback(ctx, c)
		}

		return ErrNoHandler
	}
}

// command parses "/name@bot arg arg".
func (r *Router) command(text string) (name string, args []string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}

	fields := strings.Fields(text)
	head := strings.ToLower(strings.TrimPrefix(fields[0], "/"))

	name, bot, hasBot := strings.Cut(head, "@")
	if hasBot && r.Username != "" && !strings.EqualFold(bot, r.Username) {
		return "", nil, false
	}

	return name, fields[1:], name != ""
}

// Conversations loads the conversation state of the sender before the handler
// and saves it after, when the handler changed it. key names the conversation
// (default: chat and user); the state expires after the store TTL.
func Conversations(store Store, key func(Update) string) Middleware {
	if key == nil {
		key = func(u Update) string {
			c := &Context{Update: u}

			return "chat:" + strconv.FormatInt(c.ChatID(), 10) + ":user:" + strconv.FormatInt(c.User().ID, 10)
		}
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, c *Context) error {
			k := key(c.Update)

			st, ok, err := store.Load(ctx, k)
			if err != nil {
				return fmt.Errorf("telegram: load conversation: %w", err)
			}

			if ok {
				c.state = &st
			}

			if err := next(ctx, c); err != nil {
				return err
			}

			if !c.dirty {
				return nil
			}

			if c.state == nil || (c.state.Step == "" && len(c.state.Data) == 0) {
				return store.Delete(ctx, k)
			}

			return store.Save(ctx, k, *c.state)
		}
	}
}
