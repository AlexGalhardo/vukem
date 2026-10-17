package stripe_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/contrib/stripe"
)

// FuzzConstructEvent fuzzes the Stripe-Signature header and payload: an
// event is only returned when the header carries the real v1 signature of
// that payload for a timestamp inside the tolerance.
func FuzzConstructEvent(f *testing.F) {
	const secret = "whsec_fuzz"

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	event := []byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{}}}`)

	f.Add(event, stripe.SignForTest(event, secret, now))
	f.Add(event, stripe.SignForTest(event, secret, now.Add(-10*time.Minute)))
	f.Add(event, stripe.SignForTest(event, "other", now))
	f.Add(event, "t=1,v1=00")
	f.Add(event, "v1=,t=")
	f.Add([]byte(`{}`), stripe.SignForTest([]byte(`{}`), secret, now))
	f.Add(event, stripe.SignForTest(event, secret, now)+",v1=deadbeef,t=x")

	f.Fuzz(func(t *testing.T, payload []byte, header string) {
		e, err := stripe.ConstructEvent(payload, header, secret, stripe.DefaultTolerance, now)
		if err != nil {
			return
		}

		if e.ID == "" || e.Type == "" {
			t.Fatalf("ConstructEvent(%q) = %+v without id or type", header, e)
		}

		for part := range strings.SplitSeq(header, ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
			if k != "t" {
				continue
			}

			ts, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				continue
			}

			at := time.Unix(ts, 0)
			if d := now.Sub(at); d > stripe.DefaultTolerance || d < -stripe.DefaultTolerance {
				continue
			}

			_, sig, _ := strings.Cut(stripe.SignForTest(payload, secret, at), ",v1=")
			if strings.Contains(header, "v1="+sig) {
				return
			}
		}

		t.Fatalf("ConstructEvent(%q, %q) accepted a header without a valid signature", payload, header)
	})
}
