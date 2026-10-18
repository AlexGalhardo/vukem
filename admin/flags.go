package admin

import (
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

// flagsUpdate sets (form field "value") or resets (field "reset") the
// global value of one flag, and records who changed it from what to what.
func (p *Panel) flagsUpdate(c *web.Ctx) error {
	if err := p.authorize(c, AbilityFlags, nil); err != nil {
		return err
	}

	ctx := c.Context()
	name := c.Param("name")

	var (
		ch  flags.Change
		err error
	)

	if c.FormValue("reset") != "" {
		ch, err = p.cfg.Flags.Reset(ctx, flags.Global, name)
	} else {
		ch, err = p.cfg.Flags.Set(ctx, flags.Global, name, c.FormValue("value"))
	}

	switch {
	case errors.Is(err, flags.ErrUnknown):
		return errs.New(http.StatusNotFound, "Unknown flag.")
	case errors.Is(err, flags.ErrLocked):
		// The page offers no control for locked flags: this request was
		// crafted.
		web.SecurityEvent(c.Request(), "flags.locked_write", "actor_id", p.actor(ctx), "flag", name)
		return errs.New(http.StatusForbidden, "This flag is locked in code.")
	case errors.Is(err, flags.ErrInvalid):
		return errs.New(http.StatusUnprocessableEntity, "Invalid value for this flag.")
	case err != nil:
		return err
	}

	if ch.Old == ch.New {
		// Nothing changed (same value, or a reset to the value already in
		// effect): an audit entry would only add noise.
		_ = c.Flash(FlashKey, "Flag "+ch.Flag+" unchanged.")
		return c.Redirect(p.url(FlagsResource))
	}

	actor := p.actor(ctx)
	before, after := auditValues(ch)
	web.AuditEvent(c.Request(), "flags.changed", "actor_id", actor, "flag", ch.Flag,
		"changed", true, "old", before, "new", after)

	// The store write and this row are not atomic (the store is pluggable):
	// the flag already changed, so a failed row is logged, not undone.
	if err := writeAudit(ctx, p.cfg.DB, &AuditLog{
		ActorID: actor, Resource: FlagsResource, RecordID: ch.Flag, Action: ActionUpdate,
		Changes: orm.JSON[map[string]Change]{V: map[string]Change{"value": {Before: before, After: after}}},
	}); err != nil {
		c.Log().ErrorContext(ctx, "admin: flag changed but its audit-log row failed", "actor_id", actor,
			"flag", ch.Flag, "old", before, "new", after, "err", err)
		_ = c.Flash(warningKey, "Flag "+ch.Flag+" saved, but the audit log entry failed; see the server log.")

		return c.Redirect(p.url(FlagsResource))
	}

	_ = c.Flash(FlashKey, "Flag "+ch.Flag+" saved.")

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
