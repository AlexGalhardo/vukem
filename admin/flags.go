package admin

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// FlagsResource is the audit-log resource of feature flag changes.
const FlagsResource = "_flags"

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

	actor := p.actor(ctx)
	web.AuditEvent(c.Request(), "flags.changed", "actor_id", actor, "flag", ch.Flag, "old", ch.Old, "new", ch.New)

	if err := writeAudit(ctx, p.cfg.DB, &AuditLog{
		ActorID: actor, Resource: FlagsResource, RecordID: ch.Flag, Action: ActionUpdate,
		Changes: orm.JSON[map[string]Change]{V: map[string]Change{"value": {Before: ch.Old, After: ch.New}}},
	}); err != nil {
		return err
	}

	_ = c.Flash(FlashKey, "Flag "+ch.Flag+" saved.")

	return c.Redirect(p.url(FlagsResource))
}
