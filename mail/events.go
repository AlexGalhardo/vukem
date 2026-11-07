package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EventType is a Resend email event.
type EventType string

// Resend email event types ([ParseResendEvent]).
const (
	EventSent          EventType = "email.sent"
	EventDelivered     EventType = "email.delivered"
	EventDeliveryDelay EventType = "email.delivery_delayed"
	EventBounced       EventType = "email.bounced"
	EventComplained    EventType = "email.complained"
	EventFailed        EventType = "email.failed"
	EventSuppressed    EventType = "email.suppressed"
	EventOpened        EventType = "email.opened"
	EventClicked       EventType = "email.clicked"
	EventReceived      EventType = "email.received"
)

const bouncePermanentType = "permanent"

// Event is a typed Resend webhook event for one email.
type Event struct {
	Type      EventType
	CreatedAt time.Time
	// EmailID is Resend's id for the email.
	EmailID string
	// To holds the recipients the event is about.
	To []string
	// Tags are the tags the email was sent with ([Envelope.Tags]); use them
	// to find the message in your own tables.
	Tags map[string]string
	// BounceType is "Permanent", "Transient" or "Undetermined" for
	// EventBounced; BounceMessage is Resend's explanation.
	BounceType    string
	BounceMessage string
}

// Suppress reports whether the recipients should stop receiving mail: a
// complaint, a permanent bounce, or Resend's own suppression. Feed them to
// the check given to [Mailer.SetSuppression].
func (e Event) Suppress() bool {
	switch e.Type {
	case EventComplained, EventSuppressed:
		return true
	case EventBounced:
		return strings.EqualFold(e.BounceType, bouncePermanentType)
	default:
		return false
	}
}

// ParseResendEvent decodes the body of a Resend webhook. Verify the
// request first (webhook.Standard) and call this on the verified body: it
// does not check signatures.
func ParseResendEvent(body []byte) (Event, error) {
	var raw struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			EmailID string          `json:"email_id"`
			To      []string        `json:"to"`
			Tags    json.RawMessage `json:"tags"`
			Bounce  struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"bounce"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return Event{}, fmt.Errorf("mail: resend event: %w", err)
	}

	if !strings.HasPrefix(raw.Type, "email.") {
		return Event{}, fmt.Errorf("mail: resend event: unexpected type %q", raw.Type)
	}

	tags, err := parseEventTags(raw.Data.Tags)
	if err != nil {
		return Event{}, err
	}

	return Event{
		Type: EventType(raw.Type), CreatedAt: raw.CreatedAt, EmailID: raw.Data.EmailID, To: raw.Data.To,
		Tags: tags, BounceType: raw.Data.Bounce.Type, BounceMessage: raw.Data.Bounce.Message,
	}, nil
}

// parseEventTags accepts the object form {"k":"v"} and the list form
// [{"name":"k","value":"v"}].
func parseEventTags(b json.RawMessage) (map[string]string, error) {
	if len(b) == 0 || string(b) == "null" {
		return nil, nil
	}

	var m map[string]string
	if json.Unmarshal(b, &m) == nil {
		return m, nil
	}

	var list []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	if err := json.Unmarshal(b, &list); err != nil {
		return nil, errors.New("mail: resend event: tags are neither an object nor a list")
	}

	m = make(map[string]string, len(list))
	for _, t := range list {
		m[t.Name] = t.Value
	}

	return m, nil
}
