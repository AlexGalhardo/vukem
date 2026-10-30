package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// ReasonKey is the audit-log change key holding the reason given for an
// Action with Reason set. Column names never start with "_", so it cannot
// collide with a column.
const ReasonKey = "_reason"

// Limits of custom actions.
const (
	// MaxBulk is the most records one bulk action may run on.
	MaxBulk = 100
	// MaxReason is the longest reason, in characters.
	MaxReason = 1000
)

// Action is a custom operation on one record ("approve", "reject"),
// offered as a button on the record page and, with Bulk, on the rows
// selected in the list. Every run goes through a confirmation page, needs
// the policy ability named Name on the record (method Approve for
// "approve"; a missing method denies), runs in one transaction with its
// audit-log entries and fails as a whole.
type Action[T any] struct {
	// Name is the policy ability, the URL segment and the audit-log action:
	// a lower-camel identifier of at most 16 characters ([a-z][a-zA-Z0-9]*),
	// not one of viewAny, view, create, update, delete or edit.
	Name string
	// Label is the button text (default: Name humanized).
	Label string
	// Reason asks for a reason on the confirmation page (required, at most
	// MaxReason characters); Run receives it and the audit log records it
	// under ReasonKey.
	Reason bool
	// Bulk also offers the action on the rows selected in the list (at
	// most MaxBulk at once); every record must pass the policy or none
	// runs.
	Bulk bool
	// Run performs the action. Changes it makes to rec's fields are saved
	// after it returns (orm.Update) and recorded in the audit log; other
	// writes go through tx so they roll back with the rest.
	Run func(ctx context.Context, tx *database.Tx, rec *T, reason string) error
}

var actionName = regexp.MustCompile(`^[a-z][a-zA-Z0-9]{0,15}$`)

// reservedActions are the built-in abilities and route segments.
var reservedActions = []string{AbilityViewAny, AbilityView, AbilityCreate, AbilityUpdate, AbilityDelete, "edit"}

// Actions adds custom actions; mistakes are reported by Panel.Register.
func (r *Resource[T]) Actions(actions ...Action[T]) *Resource[T] {
	for _, a := range actions {
		switch {
		case !actionName.MatchString(a.Name):
			r.fail("action name %q must match %s", a.Name, actionName)
		case slices.Contains(reservedActions, a.Name):
			r.fail("action name %q is reserved", a.Name)
		case a.Run == nil:
			r.fail("action %q has no Run", a.Name)
		case slices.ContainsFunc(r.actions, func(o Action[T]) bool { return o.Name == a.Name }):
			r.fail("duplicate action %q", a.Name)
		default:
			if a.Label == "" {
				a.Label = humanize(a.Name)
			}

			r.actions = append(r.actions, a)
		}
	}

	return r
}

type actionLink struct {
	Label, URL string
}

type actionView struct {
	Label, Action, Cancel, Reason, Error string
	AskReason                            bool
	IDs                                  []string
	Count                                int
}

// actionRoutes registers literal paths per action, so they never overlap
// the /{id}/edit and /{id}/delete patterns.
func (r *Resource[T]) actionRoutes(p *Panel, g *router.Group) {
	for _, a := range r.actions {
		g.Get("/{id}/actions/"+a.Name, r.confirmAction(p, a))
		g.Post("/{id}/actions/"+a.Name, r.runAction(p, a))

		if a.Bulk {
			g.Get("/_bulk/"+a.Name, r.confirmBulk(p, a))
			g.Post("/_bulk/"+a.Name, r.runBulk(p, a))
		}
	}
}

func (r *Resource[T]) rowActions(c *web.Ctx, p *Panel, rec *T, esc string) []actionLink {
	var out []actionLink

	for _, a := range r.actions {
		if p.can(c, a.Name, rec) {
			out = append(out, actionLink{Label: a.Label, URL: p.url(r.slug, esc, "actions", a.Name)})
		}
	}

	return out
}

func (r *Resource[T]) bulkActions(p *Panel) []actionLink {
	var out []actionLink

	for _, a := range r.actions {
		if a.Bulk {
			out = append(out, actionLink{Label: a.Label, URL: p.url(r.slug, "_bulk", a.Name)})
		}
	}

	return out
}

func (r *Resource[T]) confirmAction(p *Panel, a Action[T]) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, a.Name, rec); err != nil {
			return err
		}

		return r.renderAction(c, p, http.StatusOK, a, []*T{rec}, "", "")
	}
}

func (r *Resource[T]) runAction(p *Panel, a Action[T]) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, a.Name, rec); err != nil {
			return err
		}

		reason, msg := actionReason(c, a.Reason)
		if msg != "" {
			return r.renderAction(c, p, http.StatusUnprocessableEntity, a, []*T{rec}, reason, msg)
		}

		if err := r.run(c.Context(), p, a, []*T{rec}, reason); err != nil {
			return err
		}

		_ = c.Flash(FlashKey, a.Label+": done.")

		return c.Redirect(p.url(r.slug, url.PathEscape(r.id(reflect.ValueOf(rec).Elem()))))
	}
}

func (r *Resource[T]) confirmBulk(p *Panel, a Action[T]) web.HandlerFunc {
	return func(c *web.Ctx) error {
		recs, err := r.bulkRecords(c, p, a, c.Request().URL.Query()["ids"])
		if err != nil {
			return err
		}

		return r.renderAction(c, p, http.StatusOK, a, recs, "", "")
	}
}

func (r *Resource[T]) runBulk(p *Panel, a Action[T]) web.HandlerFunc {
	return func(c *web.Ctx) error {
		if err := c.Request().ParseForm(); err != nil {
			return errs.Wrap(http.StatusBadRequest, err)
		}

		recs, err := r.bulkRecords(c, p, a, c.Request().PostForm["ids"])
		if err != nil {
			return err
		}

		reason, msg := actionReason(c, a.Reason)
		if msg != "" {
			return r.renderAction(c, p, http.StatusUnprocessableEntity, a, recs, reason, msg)
		}

		if err := r.run(c.Context(), p, a, recs, reason); err != nil {
			return err
		}

		_ = c.Flash(FlashKey, fmt.Sprintf("%s: done for %d %s.", a.Label, len(recs), r.noun(len(recs))))

		return c.Redirect(p.url(r.slug))
	}
}

// bulkRecords loads the selected records and authorizes every one; a bad
// id, a missing record or one denial fails the whole selection.
func (r *Resource[T]) bulkRecords(c *web.Ctx, p *Panel, a Action[T], raw []string) ([]*T, error) {
	if len(raw) > MaxBulk {
		raw = nil // refuse oversized selections before deduplicating them
	}

	var ids []string

	for _, id := range raw {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 || len(ids) > MaxBulk {
		return nil, errs.New(http.StatusUnprocessableEntity,
			"select between 1 and "+strconv.Itoa(MaxBulk)+" records")
	}

	recs := make([]*T, 0, len(ids))

	for _, id := range ids {
		rec, err := r.record(c.Context(), p.cfg.DB, id)
		if err != nil {
			return nil, err
		}

		if err := p.authorize(c, a.Name, rec); err != nil {
			return nil, err
		}

		recs = append(recs, rec)
	}

	return recs, nil
}

// actionReason reads the reason field; msg is the validation message when
// it is required and missing or too long.
func actionReason(c *web.Ctx, ask bool) (reason, msg string) {
	if !ask {
		return "", ""
	}

	reason = strings.TrimSpace(c.Request().PostFormValue("reason"))

	switch {
	case reason == "":
		return reason, "A reason is required."
	case utf8.RuneCountInString(reason) > MaxReason:
		return reason, "The reason must be at most " + strconv.Itoa(MaxReason) + " characters."
	}

	return reason, ""
}

// run applies a to every record in one transaction, saving changed fields
// and writing one audit entry per record.
func (r *Resource[T]) run(ctx context.Context, p *Panel, a Action[T], recs []*T, reason string) error {
	cols := r.auditColumns()

	err := p.cfg.DB.Tx(ctx, func(tx *database.Tx) error {
		for _, rec := range recs {
			rv := reflect.ValueOf(rec).Elem()
			before := r.snapshot(rv, cols)

			if err := a.Run(ctx, tx, rec, reason); err != nil {
				return err
			}

			changes := r.diff(before, r.snapshot(rv, cols), cols)
			if len(changes) > 0 {
				if err := orm.Update(ctx, tx, rec); err != nil {
					return err
				}
			}

			if a.Reason {
				changes[ReasonKey] = Change{After: reason}
			}

			if err := writeAudit(ctx, tx, &AuditLog{
				ActorID: p.actor(ctx), Resource: r.slug, RecordID: r.id(rv), Action: a.Name,
				Changes: orm.JSON[map[string]Change]{V: changes},
			}); err != nil {
				return err
			}
		}

		return nil
	})
	if errors.Is(err, orm.ErrNotFound) {
		return orm.ErrNotFound
	}

	return err
}

func (r *Resource[T]) noun(n int) string {
	if n == 1 {
		return strings.ToLower(r.singular)
	}

	return strings.ToLower(r.plural)
}

func (r *Resource[T]) renderAction(
	c *web.Ctx, p *Panel, status int, a Action[T], recs []*T, reason, msg string,
) error {
	av := &actionView{
		Label: a.Label, AskReason: a.Reason, Reason: reason, Error: msg, Count: len(recs),
		Cancel: p.url(r.slug),
	}

	for _, rec := range recs {
		av.IDs = append(av.IDs, r.id(reflect.ValueOf(rec).Elem()))
	}

	title := fmt.Sprintf("%s %d %s", a.Label, len(recs), r.noun(len(recs)))
	av.Action = p.url(r.slug, "_bulk", a.Name)

	if c.Param("id") != "" {
		esc := url.PathEscape(av.IDs[0])
		title = a.Label + " " + r.singular + " " + av.IDs[0]
		av.Action = p.url(r.slug, esc, "actions", a.Name)
		av.Cancel = p.url(r.slug, esc)
		av.IDs = nil
	}

	data := p.base(c, title, r.slug)
	data.Resource, data.Action = r.view(c, p), av

	return p.render(c, status, "action", "", data)
}
