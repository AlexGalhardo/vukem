package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/security"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

var (
	betaFlag   = flags.Bool("beta", false).Describe("Beta features")
	lockedFlag = flags.Bool("admin_two_factor", true).Lock()
	limitFlag  = flags.Int("upload_limit_mb", 10)
	secretish  = flags.String("home_variant", "classic")
)

func flagsApp(t *testing.T) (*vukemtest.App, *flags.Flags) {
	t.Helper()

	app := newApp(t, options{flags: []flags.AnyFlag{betaFlag, lockedFlag, limitFlag, secretish}})

	ff, err := app.Flags()
	if err != nil {
		t.Fatal(err)
	}

	return app, ff
}

type audits struct {
	mu  sync.Mutex
	got []security.Audit
}

func (a *audits) list() []security.Audit { a.mu.Lock(); defer a.mu.Unlock(); return a.got }

func listenAudits(app *vukemtest.App) *audits {
	a := &audits{}
	events.Listen(app.Events, func(_ context.Context, e security.Audit) error {
		a.mu.Lock()
		a.got = append(a.got, e)
		a.mu.Unlock()

		return nil
	})

	return a
}

func TestAdminFlagsPage(t *testing.T) {
	t.Parallel()

	app, ff := flagsApp(t)
	got := listenAudits(app)
	c := as(t, app, "admin")

	c.Get("/admin").AssertOK().AssertSee("Feature flags")
	page := c.Get("/admin/_flags").AssertOK()
	for _, want := range []string{"beta", "Beta features", "Turn on", "locked in code", "upload_limit_mb"} {
		page.AssertSee(want)
	}

	c.PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertRedirect("/admin/_flags")

	if !ff.Enabled(t.Context(), betaFlag) {
		t.Fatal("beta is still off after the toggle")
	}

	c.PostForm("/admin/_flags/upload_limit_mb", url.Values{"value": {"25"}}).AssertRedirect("/admin/_flags")
	c.Get("/admin/_flags").AssertOK().AssertSee("Turn off").AssertSee("Reset to default")
	c.PostForm("/admin/_flags/beta", url.Values{"reset": {"1"}}).AssertRedirect("/admin/_flags")

	if ff.Enabled(t.Context(), betaFlag) || flags.Get(t.Context(), ff, limitFlag) != 25 {
		t.Error("reset or the int value did not stick")
	}

	type entry struct{ Resource, Record, Before, After string }

	var logs []entry
	for _, l := range auditLogs(t, app) {
		ch := l.Changes.V["value"]
		logs = append(logs, entry{l.Resource, l.RecordID, fmt.Sprint(ch.Before), fmt.Sprint(ch.After)})
	}

	want := []entry{
		{admin.FlagsResource, "beta", "false", "true"},
		{admin.FlagsResource, "upload_limit_mb", "10", "25"},
		{admin.FlagsResource, "beta", "true", "false"},
	}
	if diff := cmp.Diff(want, logs); diff != "" {
		t.Errorf("audit log (-want +got):\n%s", diff)
	}

	recorded := got.list()
	if len(recorded) != 3 || recorded[0].Name != "flags.changed" || recorded[0].Attrs["flag"] != "beta" ||
		recorded[0].Attrs["old"] != "false" || recorded[0].Attrs["new"] != "true" || recorded[0].Attrs["actor_id"] == "" {
		t.Errorf("audit events = %+v, want three flags.changed with actor, flag, old and new", recorded)
	}

	c.Get("/admin/_audit?resource=_flags").AssertOK().AssertSee("upload_limit_mb")

	c.PostForm("/admin/_flags/ghost", url.Values{"value": {"true"}}).AssertNotFound()
	c.PostForm("/admin/_flags/upload_limit_mb", url.Values{"value": {"many"}}).
		AssertStatus(http.StatusUnprocessableEntity)
}

func TestSecurity_A01_AdminFlagsRequireAbilityAndRespectLocks(t *testing.T) {
	t.Parallel()

	app, ff := flagsApp(t)

	// editor passes the panel gate but has no manageFlags ability.
	editor := as(t, app, "editor")
	editor.Get("/admin").AssertOK().AssertDontSee("Feature flags")
	editor.Get("/admin/_flags").AssertForbidden()
	editor.PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertForbidden()

	app.Client(t).WithHeader("Sec-Fetch-Site", "same-origin").
		PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertRedirect("/login")

	boss := as(t, app, "admin")
	boss.PostForm("/admin/_flags/admin_two_factor", url.Values{"value": {"false"}}).AssertForbidden()
	boss.PostForm("/admin/_flags/admin_two_factor", url.Values{"reset": {"1"}}).AssertForbidden()

	// A cross-site form post is refused before the handler runs.
	app.Client(t).
		WithHeader("Sec-Fetch-Site", "cross-site").
		ActingAs("web", createUser(t, app, "x@example.com", "admin")).
		PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).
		AssertForbidden()

	if ff.Enabled(t.Context(), betaFlag) || !ff.Enabled(t.Context(), lockedFlag) {
		t.Error("a refused request changed a flag")
	}

	if n := len(auditLogs(t, app)); n != 0 {
		t.Errorf("audit log has %d entries after refused requests, want 0", n)
	}
}

func TestAdminWithoutFlagsHasNoFlagsPage(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{})
	c := as(t, app, "admin")

	c.Get("/admin").AssertOK().AssertDontSee("Feature flags")
	c.Get("/admin/_flags").AssertNotFound()
}

func TestSecurity_A09_AdminFlagsRedactStringValues(t *testing.T) {
	t.Parallel()

	app, _ := flagsApp(t)
	got := listenAudits(app)
	c := as(t, app, "admin")

	c.PostForm("/admin/_flags/home_variant", url.Values{"value": {"hunter2-campaign"}}).AssertRedirect("/admin/_flags")

	logs := auditLogs(t, app)
	if len(logs) != 1 {
		t.Fatalf("audit log = %+v, want one entry", logs)
	}

	ch := logs[0].Changes.V["value"]
	before, after := fmt.Sprint(ch.Before), fmt.Sprint(ch.After)

	recorded := got.list()
	if len(recorded) != 1 {
		t.Fatalf("audit events = %+v, want one", recorded)
	}

	ev := fmt.Sprint(recorded[0].Attrs)
	for _, s := range []string{before, after, ev} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "classic") {
			t.Errorf("a string flag value reached the audit trail in clear: %q", s)
		}
	}

	if !strings.HasPrefix(after, "16 bytes, sha256:") || recorded[0].Attrs["changed"] != true {
		t.Errorf("audit after = %q, event = %v, want the length, a SHA-256 prefix and changed=true", after, ev)
	}
}

func TestAdminFlagsAuditFailureStillRedirects(t *testing.T) {
	t.Parallel()

	app, ff := flagsApp(t)
	c := as(t, app, "admin")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "DROP TABLE "+admin.AuditTable); err != nil {
		t.Fatal(err)
	}

	c.PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertRedirect("/admin/_flags")
	c.Get("/admin/_flags").AssertOK().AssertSee("audit log entry failed")

	if !ff.Enabled(t.Context(), betaFlag) {
		t.Error("the flag did not change")
	}
}

func TestAdminFlagsWithNoFlagsRegisteredHasNoRoutes(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{flags: []flags.AnyFlag{}})
	c := as(t, app, "admin")

	c.Get("/admin").AssertOK().AssertDontSee("Feature flags")
	c.Get("/admin/_flags").AssertNotFound()
	c.PostForm("/admin/_flags/beta", url.Values{"value": {"true"}}).AssertNotFound()
}

func TestAdminFlagsUnchangedValueIsNotAudited(t *testing.T) {
	t.Parallel()

	app, _ := flagsApp(t)
	got := listenAudits(app)
	c := as(t, app, "admin")

	// beta is already false: saving false again changes nothing.
	c.PostForm("/admin/_flags/beta", url.Values{"value": {"false"}}).AssertRedirect("/admin/_flags")
	c.PostForm("/admin/_flags/beta", url.Values{"reset": {"1"}}).AssertRedirect("/admin/_flags")

	if n := len(auditLogs(t, app)); n != 0 {
		t.Errorf("audit log has %d entries for no-op saves, want 0", n)
	}

	if n := len(got.list()); n != 0 {
		t.Errorf("%d audit events for no-op saves, want 0", n)
	}
}
