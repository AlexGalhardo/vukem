package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/broadcast"
)

// Subscription binds a field of the subscription root type to a source
// stream (spec §6.2.3 CreateSourceEventStream): fn runs once per
// subscribe with the decoded arguments and returns a channel of events.
// Every value received is the value of the field for that event and is
// completed per the selection set (ExecuteSubscriptionEvent), so a
// subscriber gets one response per event.
//
// fn owns the channel: it closes it when the stream ends (the client then
// gets "complete") and must stop sending once ctx is done, which happens
// when the client unsubscribes or disconnects. An error from fn is a field
// error of the subscribe response (a 403 errs.HTTPError → FORBIDDEN).
func Subscription[A, O any](
	fieldName string, fn func(ctx context.Context, args A) (<-chan O, error), opts ...FieldOption,
) Option {
	return optionFunc(func(b *builder) error {
		typeName := b.rootName(opSubscription)

		decode, names, err := argsDecoder[A]()
		if err != nil {
			return fmt.Errorf("graphql: binding %s.%s: %w", typeName, fieldName, err)
		}

		bd := binding{typeName: typeName, field: fieldName, argNames: names, out: reflect.TypeFor[O]()}
		// The event is the field value: the root field resolves to its parent.
		bd.resolve = func(_ context.Context, parent any, _ map[string]any) (any, error) { return parent, nil }
		bd.subscribe = func(ctx context.Context, args map[string]any) (func(context.Context) (any, bool), error) {
			a, err := decode(args)
			if err != nil {
				return nil, err
			}

			ch, err := fn(ctx, a)
			if err != nil {
				return nil, err
			}

			if ch == nil {
				return nil, fmt.Errorf("subscription %s.%s returned a nil channel", typeName, fieldName)
			}

			return func(ctx context.Context) (any, bool) {
				select {
				case v, ok := <-ch:
					return v, ok
				case <-ctx.Done():
					return nil, false
				}
			}, nil
		}

		for _, o := range opts {
			o(&bd)
		}

		b.bindings = append(b.bindings, bd)

		return nil
	})
}

// FromBroadcast returns a Subscription source stream fed by broadcast
// channels: channels names the channels for the arguments, and every
// message on them is decoded from its JSON data into O (or passed as is
// when O is broadcast.Message). With events, only messages with those
// event names are delivered; presence notifications are skipped unless
// listed. Messages whose data does not decode into O are dropped.
//
// The channels are subscribed through b.Subscribe in the context of the
// subscription (the connection's, after WSConfig.OnConnect), so the
// public, private and presence rules of b apply and fail closed: a denied
// channel fails the subscription with FORBIDDEN.
//
//	graphql.Subscription("postCreated", graphql.FromBroadcast[graphql.NoArgs, Post](b,
//		func(context.Context, graphql.NoArgs) ([]string, error) { return []string{"posts"}, nil },
//		"post.created"))
//
//nolint:gocognit // one goroutine pump with cancel, backpressure and close paths that share state
func FromBroadcast[A, O any](
	b *broadcast.Server, channels func(ctx context.Context, args A) ([]string, error), events ...string,
) func(context.Context, A) (<-chan O, error) {
	raw := reflect.TypeFor[O]() == reflect.TypeFor[broadcast.Message]()

	return func(ctx context.Context, a A) (<-chan O, error) {
		if b == nil || channels == nil {
			return nil, errors.New("graphql: FromBroadcast needs a broadcast server and a channels func")
		}

		names, err := channels(ctx, a)
		if err != nil {
			return nil, err
		}

		sub, err := b.Subscribe(ctx, names...)
		if err != nil {
			return nil, err
		}

		out := make(chan O)

		go func() {
			defer close(out)
			defer sub.Close()

			for {
				var m broadcast.Message

				select {
				case msg, ok := <-sub.Messages():
					if !ok {
						return
					}

					m = msg
				case <-ctx.Done():
					return
				}

				if !wanted(m.Event, events) {
					continue
				}

				var v O
				if raw {
					v, _ = any(m).(O)
				} else if json.Unmarshal(m.Data, &v) != nil {
					continue
				}

				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}()

		return out, nil
	}
}

func wanted(event string, events []string) bool {
	if len(events) > 0 {
		return slices.Contains(events, event)
	}

	return !strings.HasPrefix(event, "presence:")
}

// Subscribe executes req as a stream of responses. A subscription yields
// one response per source event until the source ends or ctx is done;
// queries and mutations yield their single response. When the request
// fails before the stream starts (parse, validation, variables, limits,
// or the source stream itself), Subscribe returns a nil stream and the
// response carrying the errors.
//
// Cancel ctx to stop a subscription: the source stream is bound to it.
func (s *Schema) Subscribe(ctx context.Context, req Request) (iter.Seq[*Response], *Response) {
	p, errs := s.prepare(req, true)
	if errs != nil {
		return nil, &Response{Errors: errs}
	}

	return s.subscribe(ctx, p)
}

// subscribe implements Subscribe (spec §6.2.3) for a prepared request.
func (s *Schema) subscribe(ctx context.Context, p *prepared) (iter.Seq[*Response], *Response) {
	if p.op.kind != opSubscription {
		resp := s.run(ctx, p)
		return func(yield func(*Response) bool) { yield(resp) }, nil
	}

	e := s.newExecutor(p)

	groups := e.collectFields(s.subscr, p.op.sel, map[string]bool{}, nil)
	if len(groups) != 1 {
		return nil, &Response{Errors: []*Error{validationError(subscriptionMessage(p.op), p.op.pos)}}
	}

	g := groups[0]
	fp := (*path)(nil).with(g.key)

	fd := s.fieldFor(s.subscr, g.fields[0].name)
	if fd == nil || fd.subscribe == nil {
		return nil, &Response{Errors: []*Error{validationError(
			fmt.Sprintf("Field %q cannot be the root of a subscription.", g.fields[0].name), g.fields[0].pos)}}
	}

	args, err := e.arguments(fd, g.fields[0])
	if err == nil {
		var next func(context.Context) (any, bool)
		if next, err = s.sourceStream(ctx, fd, args); err == nil {
			return s.eventStream(ctx, p, next), nil
		}
	}

	e.addError(ctx, err, g.fields, fp)

	return nil, &Response{Errors: e.errs}
}

func (s *Schema) sourceStream(
	ctx context.Context, fd *fieldType, args map[string]any,
) (next func(context.Context) (any, bool), err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.ErrorContext(ctx, "graphql: subscription panic", "field", fd.parent.name+"."+fd.name, "panic", r,
				"stack", string(debug.Stack()))
			err = fmt.Errorf("subscription %s.%s panicked: %v", fd.parent.name, fd.name, r)
		}
	}()

	return fd.subscribe(ctx, args)
}

// eventStream maps source events to responses (spec §6.2.3.2
// MapSourceToResponseEvent).
func (s *Schema) eventStream(
	ctx context.Context,
	p *prepared,
	next func(context.Context) (any, bool),
) iter.Seq[*Response] {
	return func(yield func(*Response) bool) {
		for {
			ev, ok := next(ctx)
			if !ok {
				return
			}

			if !yield(s.execute(ctx, p, s.subscr, ev, false)) {
				return
			}
		}
	}
}
