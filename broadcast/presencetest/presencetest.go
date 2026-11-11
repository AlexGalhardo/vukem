// Package presencetest is the conformance suite every
// broadcast.PresenceStore passes (the memory store in core, Redis in
// drivers/redis). A new store calls [Run] from one of its tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package presencetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/broadcast"
)

// Options adapts the suite to a store.
type Options struct {
	// TTL is the store's connection expiry; zero skips the expiry checks
	// (the memory store never expires). Use at least 2 seconds: the expiry
	// checks wait on the real clock and a shorter TTL makes them flaky.
	TTL time.Duration
}

const concurrentJoins = 32

type testCase struct {
	name string
	fn   func(*testing.T, broadcast.PresenceStore)
}

// Run checks store. Every subtest uses its own random channel names, so the
// suite can share a server with other tests.
func Run(t *testing.T, store broadcast.PresenceStore, o Options) {
	t.Helper()

	cases := []testCase{
		{"FirstJoinReportsFirst", firstJoinReportsFirst},
		{"SecondConnectionOfSameMemberIsNotFirst", secondConnectionIsNotFirst},
		{"LeaveOfOneOfTwoConnectionsIsNotLast", leaveOfOneOfTwoIsNotLast},
		{"LeaveOfLastConnectionIsLast", leaveOfLastIsLast},
		{"LeaveUnknownConnection", leaveUnknownConnection},
		{"MembersSortedOnePerID", membersSortedOnePerID},
		{"ChannelsAreIsolated", channelsAreIsolated},
		{"InfoRoundTripsAsJSON", infoRoundTripsAsJSON},
		{"ConcurrentJoinsReportFirstOnce", concurrentJoinsReportFirstOnce},
		{"LeaveOfOneMemberKeepsTheOthers", leaveKeepsTheOthers},
		{"LeaveTwiceReportsLastOnce", leaveTwiceReportsLastOnce},
		{"JoinOfTheSameConnectionTwice", joinSameConnectionTwice},
		{"TouchOfAnUnknownConnectionDoesNotRecreateIt", touchDoesNotRecreate},
	}

	if o.TTL > 0 {
		cases = append(
			cases,
			testCase{
				"UntouchedConnectionExpires",
				func(t *testing.T, s broadcast.PresenceStore) {
					t.Helper()
					untouchedExpires(t, s, o.TTL)
				},
			},
			testCase{
				"TouchedConnectionSurvives",
				func(t *testing.T, s broadcast.PresenceStore) {
					t.Helper()
					touchedSurvives(t, s, o.TTL)
				},
			},
		)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.fn(t, store) })
	}
}

func channelName() string { return "presence-ssetest-" + rand.Text()[:8] }

func join(t *testing.T, s broadcast.PresenceStore, ch, conn string, m broadcast.Member) bool {
	t.Helper()

	first, err := s.Join(t.Context(), ch, conn, m)
	if err != nil {
		t.Fatalf("Join(%q, %q, %q) = %v", ch, conn, m.ID, err)
	}

	return first
}

func leave(t *testing.T, s broadcast.PresenceStore, ch, conn, id string) bool {
	t.Helper()

	last, err := s.Leave(t.Context(), ch, conn, id)
	if err != nil {
		t.Fatalf("Leave(%q, %q, %q) = %v", ch, conn, id, err)
	}

	return last
}

func ids(t *testing.T, s broadcast.PresenceStore, ch string) []string {
	t.Helper()

	members, err := s.Members(t.Context(), ch)
	if err != nil {
		t.Fatalf("Members(%q) = %v", ch, err)
	}

	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.ID)
	}

	return out
}

func leaveKeepsTheOthers(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})
	join(t, s, ch, "c2", broadcast.Member{ID: "bob"})

	if !leave(t, s, ch, "c2", "bob") {
		t.Error("bob's only connection was not reported as last")
	}

	if got := ids(t, s, ch); !slices.Equal(got, []string{"ana"}) {
		t.Errorf("members = %v, want only ana", got)
	}
}

func leaveTwiceReportsLastOnce(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	if !leave(t, s, ch, "c1", "ana") {
		t.Fatal("the first Leave of the last connection was not reported as last")
	}

	if leave(t, s, ch, "c1", "ana") {
		t.Error("the second Leave of the same connection was reported as last again")
	}
}

func joinSameConnectionTwice(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	if join(t, s, ch, "c1", broadcast.Member{ID: "ana"}) {
		t.Error("joining again with the same connection was reported as first")
	}

	if !leave(t, s, ch, "c1", "ana") {
		t.Error("one Leave after a repeated Join was not the last: the connection is counted once")
	}

	if got := ids(t, s, ch); len(got) != 0 {
		t.Errorf("members = %v, want none", got)
	}
}

// touchDoesNotRecreate: a Touch that arrives after Leave (a heartbeat in
// flight) must not bring the member back.
func touchDoesNotRecreate(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()

	if err := s.Touch(t.Context(), ch, "ghost"); err != nil {
		t.Fatalf("Touch of an unknown connection = %v, want nil", err)
	}

	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})
	leave(t, s, ch, "c1", "ana")

	if err := s.Touch(t.Context(), ch, "c1"); err != nil {
		t.Fatalf("Touch after Leave = %v, want nil", err)
	}

	if got := ids(t, s, ch); len(got) != 0 {
		t.Errorf("members = %v after a Touch following Leave, want none", got)
	}

	if !join(t, s, ch, "c2", broadcast.Member{ID: "ana"}) {
		t.Error("a Join after the late Touch was not reported as first")
	}
}

func firstJoinReportsFirst(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()

	if !join(t, s, ch, "c1", broadcast.Member{ID: "ana"}) {
		t.Error("the first connection of a member was not reported as first")
	}
}

func secondConnectionIsNotFirst(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	if join(t, s, ch, "c2", broadcast.Member{ID: "ana"}) {
		t.Error("a second connection of the same member was reported as first")
	}
}

func leaveOfOneOfTwoIsNotLast(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})
	join(t, s, ch, "c2", broadcast.Member{ID: "ana"})

	if leave(t, s, ch, "c1", "ana") {
		t.Error("leaving one of two connections was reported as last")
	}

	if got := ids(t, s, ch); !slices.Equal(got, []string{"ana"}) {
		t.Errorf("members = %v, want ana still present", got)
	}
}

func leaveOfLastIsLast(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	if !leave(t, s, ch, "c1", "ana") {
		t.Error("leaving the last connection was not reported as last")
	}

	if got := ids(t, s, ch); len(got) != 0 {
		t.Errorf("members = %v, want none", got)
	}

	if !join(t, s, ch, "c9", broadcast.Member{ID: "ana"}) {
		t.Error("joining again after the last leave was not reported as first")
	}
}

func leaveUnknownConnection(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()

	if leave(t, s, ch, "ghost", "ana") {
		t.Error("leaving an unknown connection was reported as last")
	}

	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	if leave(t, s, ch, "ghost", "ana") {
		t.Error("leaving an unknown connection of a present member was reported as last")
	}

	if got := ids(t, s, ch); !slices.Equal(got, []string{"ana"}) {
		t.Errorf("members = %v, want ana untouched", got)
	}
}

func membersSortedOnePerID(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "carol"})
	join(t, s, ch, "c2", broadcast.Member{ID: "ana"})
	join(t, s, ch, "c3", broadcast.Member{ID: "bob"})
	join(t, s, ch, "c4", broadcast.Member{ID: "ana"})

	if got := ids(t, s, ch); !slices.Equal(got, []string{"ana", "bob", "carol"}) {
		t.Errorf("members = %v, want [ana bob carol] once each", got)
	}
}

func channelsAreIsolated(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	one, two := channelName(), channelName()
	join(t, s, one, "c1", broadcast.Member{ID: "ana"})

	if got := ids(t, s, two); len(got) != 0 {
		t.Errorf("members of another channel = %v, want none", got)
	}

	if !join(t, s, two, "c1", broadcast.Member{ID: "ana"}) {
		t.Error("the same member and connection on another channel was not first there")
	}

	leave(t, s, one, "c1", "ana")

	if got := ids(t, s, two); !slices.Equal(got, []string{"ana"}) {
		t.Errorf("members = %v, want ana still present on the other channel", got)
	}
}

// infoRoundTripsAsJSON compares JSON: a remote store returns Info decoded
// as map[string]any, not as the original type.
func infoRoundTripsAsJSON(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()
	want := broadcast.Member{
		ID:   "ana",
		Info: map[string]any{"name": "Ana é \"quoted\"", "age": 30, "tags": []string{"a", "b"}},
	}
	join(t, s, ch, "c1", want)

	members, err := s.Members(t.Context(), ch)
	if err != nil || len(members) != 1 {
		t.Fatalf("Members = %v, %v", members, err)
	}

	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	gotJSON, err := json.Marshal(members[0])
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(wantJSON, gotJSON) {
		t.Errorf("member JSON = %s, want %s", gotJSON, wantJSON)
	}
}

func concurrentJoinsReportFirstOnce(t *testing.T, s broadcast.PresenceStore) {
	t.Helper()

	ch := channelName()

	var (
		wg     sync.WaitGroup
		firsts atomic.Int32
		failed atomic.Int32
	)

	for i := range concurrentJoins {
		wg.Go(func() {
			first, err := s.Join(
				context.WithoutCancel(t.Context()),
				ch,
				"conn-"+strconv.Itoa(i),
				broadcast.Member{ID: "ana"},
			)
			if err != nil {
				failed.Add(1)
				return
			}

			if first {
				firsts.Add(1)
			}
		})
	}

	wg.Wait()

	if failed.Load() != 0 {
		t.Fatalf("%d joins failed", failed.Load())
	}

	if firsts.Load() != 1 {
		t.Errorf("%d joins reported first, want exactly 1", firsts.Load())
	}
}

// wait blocks for d without time.Sleep.
func wait(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case <-time.After(d):
	case <-t.Context().Done():
	}
}

func untouchedExpires(t *testing.T, s broadcast.PresenceStore, ttl time.Duration) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	wait(t, ttl+ttl/2)

	if got := ids(t, s, ch); len(got) != 0 {
		t.Errorf("members = %v after the TTL without a Touch, want none", got)
	}

	if !join(t, s, ch, "c2", broadcast.Member{ID: "ana"}) {
		t.Error("a join after the expiry was not reported as first")
	}
}

func touchedSurvives(t *testing.T, s broadcast.PresenceStore, ttl time.Duration) {
	t.Helper()

	ch := channelName()
	join(t, s, ch, "c1", broadcast.Member{ID: "ana"})

	for range 6 {
		wait(t, ttl/4)

		if err := s.Touch(t.Context(), ch, "c1"); err != nil {
			t.Fatalf("Touch = %v", err)
		}
	}

	// 1.5 TTL has passed; only the Touch calls kept the connection.
	if got := ids(t, s, ch); !slices.Equal(got, []string{"ana"}) {
		t.Errorf("members = %v after 1.5 TTL of Touch calls, want ana", got)
	}
}
