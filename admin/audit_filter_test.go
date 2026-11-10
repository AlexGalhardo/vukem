package admin_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/orm"
)

func TestSecurity_A01_AdminAuditFilteredByResourcePolicy(t *testing.T) {
	t.Parallel()

	// Note has no policy, so the reader may not view it; Post is viewable.
	app := newApp(t, options{})
	c := as(t, app, "admin")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range []admin.AuditLog{
		{ActorID: "1", Resource: "posts", RecordID: "1", Action: admin.ActionCreate},
		{ActorID: "1", Resource: "notes", RecordID: "9", Action: admin.ActionCreate},
	} {
		if err := orm.Create(t.Context(), db, &e); err != nil {
			t.Fatal(err)
		}
	}

	for _, target := range []string{"/admin/_audit", "/admin/_audit?resource=notes"} {
		c.GetJSON(target).AssertOK().AssertDontSee(`"notes"`)
		c.Get(target).AssertOK().AssertDontSee(">notes<")
	}

	c.GetJSON("/admin/_audit").AssertJSONCount("entries", 1)
}

func TestSecurity_A09_AdminAuditRedactsOnRead(t *testing.T) {
	t.Parallel()

	// "body" was logged in clear before it became hidden.
	app := newApp(t, options{resource: func() []admin.AnyResource {
		return []admin.AnyResource{admin.NewResource[Post](postSchema).Hidden("body")}
	}})
	c := as(t, app, "admin")

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	e := admin.AuditLog{ActorID: "1", Resource: "posts", RecordID: "1", Action: admin.ActionUpdate}
	e.Changes.V = map[string]admin.Change{
		"body":  {Before: "old-clear-body", After: "new-clear-body"},
		"title": {Before: "Old title", After: "New title"},
	}

	if err := orm.Create(t.Context(), db, &e); err != nil {
		t.Fatal(err)
	}

	c.Get("/admin/_audit").AssertOK().AssertSee("New title").AssertDontSee("clear-body").AssertSee("redacted")
	c.GetJSON("/admin/_audit").AssertOK().AssertSee("New title").AssertDontSee("clear-body").
		AssertJSONPath("entries.0.changes.0.redacted", true)
}
