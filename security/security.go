// Package security defines the security event dispatched for every
// security-relevant decision (failed logins, denied authorization, CSRF
// rejections, rate limits, invalid signatures). Subscribe to alert:
//
//	events.Listen(app.Events, func(ctx context.Context, e security.Event) error {
//		if e.Name == "auth.login_failed" { ... }
//		return nil
//	})
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
