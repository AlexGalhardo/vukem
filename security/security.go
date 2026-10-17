// Package security defines the security event dispatched for every
// security-relevant decision (failed logins, denied authorization, CSRF
// rejections, rate limits, invalid signatures). Subscribe to alert:
//
//	events.Listen(app.Events, func(ctx context.Context, e security.Event) error {
//		if e.Name == "auth.login_failed" { ... }
//		return nil
//	})
//
// Handlers and middleware emit events through web.SecurityEvent; each one
// is logged and, when the app has an event bus, dispatched as an [Event].
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/security.md.
package security

import "time"

// Event describes one security event. Attrs never contain secrets.
type Event struct {
	Name      string         `json:"name"`
	At        time.Time      `json:"at"`
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	IP        string         `json:"ip"`
	RequestID string         `json:"request_id,omitempty"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

// Audit describes one routine audit record emitted through web.AuditEvent
// (a payout recorded, a role granted). It has the fields of [Event] but is
// a distinct type, so listeners alerting on Event never see audit records:
//
//	events.Listen(app.Events, func(ctx context.Context, a security.Audit) error { ... })
//
// Attrs never contain secrets.
type Audit Event
