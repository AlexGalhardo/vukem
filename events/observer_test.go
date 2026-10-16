package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/events"
)

type orderPlaced struct{ ID int }

type eventLog struct{ got []events.Event }

func (l *eventLog) ObserveEvent(_ context.Context, e events.Event) {
	e.Duration = 0
	l.got = append(l.got, e)
}

func TestObserverSeesDispatches(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	bus := events.New(nil, 1)
	bus.Register(
		events.On(func(context.Context, orderPlaced) error { return nil }),
		events.On(func(_ context.Context, e orderPlaced) error {
			if e.ID == 2 {
				return boom
			}

			return nil
		}),
	)

	obs := &eventLog{}
	bus.SetObserver(obs)

	_ = bus.Dispatch(t.Context(), orderPlaced{ID: 1})
	err := bus.Dispatch(t.Context(), orderPlaced{ID: 2})
	_ = bus.Dispatch(t.Context(), "no listeners")

	if len(obs.got) != 3 || !errors.Is(obs.got[1].Err, boom) || !errors.Is(err, boom) {
		t.Fatalf("observed = %+v", obs.got)
	}

	obs.got[1].Err = nil
	want := []events.Event{
		{Type: "events_test.orderPlaced", Listeners: 2},
		{Type: "events_test.orderPlaced", Listeners: 2},
		{Type: "string", Listeners: 0},
	}

	if diff := cmp.Diff(want, obs.got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}
