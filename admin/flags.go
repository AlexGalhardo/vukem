package admin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// FlagsResource is the audit-log resource of feature flag changes.
const FlagsResource = "_flags"

// warningKey is the session flash key of the panel's warnings.
const warningKey = "admin.warning"

type flagsView struct {
	Rows []flagRow
}

type flagRow struct {
	flags.State

	Action string
	// On is the parsed Value of a boolean flag.
	On bool
}

func (p *Panel) hasFlags() bool { return p.cfg.Flags != nil && len(p.cfg.Flags.All()) > 0 }

// flagsPage lists the global value of every registered flag.
func (p *Panel) flagsPage(c *web.Ctx) error {
	if err := p.authorize(c, AbilityFlags, nil); err != nil {
		return err
	}

	states, err := p.cfg.Flags.States(c.Context(), flags.Global)
	if err != nil {
		// Showing defaults here would invite toggles based on wrong values.
		return errs.Wrap(http.StatusServiceUnavailable, err)
	}

	fv := &flagsView{}
	for _, s := range states {
		fv.Rows = append(fv.Rows, flagRow{
			State: s, Action: p.url(FlagsResource, url.PathEscape(s.Name)), On: s.Value == "true",
		})
	}

	data := p.base(c, "Feature flags", FlagsResource)
	data.Flags = fv

	return p.render(c, http.StatusOK, "flags", "", data)
}

// ErrAuditWrite is wrapped by the error ChangeFlag and Audit return when the audit-log row could not be written.
// For ChangeFlag the flag has already changed: the store and the row are not atomic (the store is pluggable).
var ErrAuditWrite = errors.New("admin: audit log write failed")

// Audit appends one entry to the audit log as the signed-in user of ctx: the same row the panel writes for its own
// resources, so an app that serves its own screens (using Guard without Mount) keeps one log. Changes maps columns
// to their before and after values; mark a sensitive column with Change{Redacted: true} so no value is stored.
func (p *Panel) Audit(ctx context.Context, resource, recordID, action string, changes map[string]Change) error {
	err := writeAudit(ctx, p.cfg.DB, &AuditLog{
		ActorID: p.actor(ctx), Resource: resource, RecordID: recordID, Action: action,
		Changes: orm.JSON[map[string]Change]{V: changes},
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuditWrite, err)
	}

	return nil
}

// ChangeFlag sets the global value of a flag, or resets it to its default when reset is true, and audits the change
// (string values are recorded as a length and a hash prefix only). It checks AbilityFlags itself and fails closed
// without an Authorizer, so it is safe in an app's own handler behind Guard. Unknown, locked and invalid flags
// return an *errs.Error with status 404, 403 and 422. When nothing changed the Change has Old == New and no row is
// written. A failed audit row returns the Change together with an error wrapping ErrAuditWrite.
func (p *Panel) ChangeFlag(c *web.Ctx, name, value string, reset bool) (flags.Change, error) {
	ctx, r := c.Context(), c.Request()

	if p.cfg.Flags == nil {
		return flags.Change{}, errs.ErrNotFound
	}

	if err := p.authorize(c, AbilityFlags, nil); err != nil {
		return flags.Change{}, err
	}

	var (
		ch  flags.Change
		err error
	)

	if reset {
		ch, err = p.cfg.Flags.Reset(ctx, flags.Global, name)
	} else {
		ch, err = p.cfg.Flags.Set(ctx, flags.Global, name, value)
	}

	switch {
	case errors.Is(err, flags.ErrUnknown):
		return ch, errs.New(http.StatusNotFound, "Unknown flag.")
	case errors.Is(err, flags.ErrLocked):
		// The page offers no control for locked flags: this request was crafted.
		web.SecurityEvent(r, "flags.locked_write", "actor_id", p.actor(ctx), "flag", name)

		return ch, errs.New(http.StatusForbidden, "This flag is locked in code.")
	case errors.Is(err, flags.ErrInvalid):
		return ch, errs.New(http.StatusUnprocessableEntity, "Invalid value for this flag.")
	case err != nil:
		return ch, err
	}

	if ch.Old == ch.New {
		// Nothing changed (same value, or a reset to the value already in effect): an audit entry would only add
		// noise.
		return ch, nil
	}

	actor := p.actor(ctx)
	before, after := auditValues(ch)
	web.AuditEvent(r, "flags.changed", "actor_id", actor, "flag", ch.Flag, "changed", true, "old", before, "new", after)

	if err := p.Audit(ctx, FlagsResource, ch.Flag, ActionUpdate,
		map[string]Change{"value": {Before: before, After: after}}); err != nil {
		return ch, err
	}

	return ch, nil
}

// flagsUpdate is the handler of the panel's flags page: it sets (form field "value") or resets (field "reset") the
// global value of one flag through ChangeFlag.
func (p *Panel) flagsUpdate(c *web.Ctx) error {
	ch, err := p.ChangeFlag(c, c.Param("name"), c.FormValue("value"), c.FormValue("reset") != "")

	switch {
	case errors.Is(err, ErrAuditWrite):
		c.Log().ErrorContext(c.Context(), "admin: flag changed but its audit-log row failed",
			"actor_id", p.actor(c.Context()), "flag", ch.Flag, "err", err)
		_ = c.Flash(warningKey, "Flag "+ch.Flag+" saved, but the audit log entry failed; see the server log.")
	case err != nil:
		return err
	case ch.Old == ch.New:
		_ = c.Flash(FlashKey, "Flag "+ch.Flag+" unchanged.")
	default:
		_ = c.Flash(FlashKey, "Flag "+ch.Flag+" saved.")
	}

	return c.Redirect(p.url(FlagsResource))
}

// auditValues returns what logs and the audit log record of a change: the
// values of bool and int flags, and only the length and a SHA-256 prefix of
// string values, which may hold anything.
func auditValues(ch flags.Change) (before, after string) {
	if !ch.Sensitive {
		return ch.Old, ch.New
	}

	return digest(ch.Old), digest(ch.New)
}

func digest(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%d bytes, sha256:%x", len(v), sum[:4])
}
