package admin_test

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/z"
)

type PinInput struct {
	Title  string `form:"title"`
	Status string `form:"status"`
	Pin    bool   `form:"pin"`
}

var pinSchema = z.Struct[PinInput](z.Shape{
	"title":  z.String().Min(3),
	"status": z.Enum("draft", "live", "pinned"),
	"pin":    z.Bool().Optional(),
})

// saveLog records what the AfterSave hook saw; committed collects the
// AfterCommit callbacks that ran.
type saveLog struct {
	mu        sync.Mutex
	saves     []admin.Saved[Post]
	committed []string
}

func (l *saveLog) resource(fail bool) func() []admin.AnyResource {
	return func() []admin.AnyResource {
		return []admin.AnyResource{
			admin.NewResource[Post](pinSchema).
				Virtual("pin", func(p *Post) any { return p.Status == "pinned" }).
				AfterSave(func(ctx context.Context, s admin.Saved[Post]) error {
					l.mu.Lock()
					l.saves = append(l.saves, s)
					l.mu.Unlock()

					if pin, _ := s.Virtual["pin"].(bool); pin {
						// A side effect in the panel transaction: no deadlock on
						// the single SQLite writer, and rolled back with the save.
						s.Record.Status = "pinned"
						if err := orm.Update(ctx, s.Tx, s.Record); err != nil {
							return err
						}
					}

					if fail {
						return errors.New("hook failed")
					}

					title := s.Record.Title
					s.Tx.AfterCommit(func(context.Context) {
						l.mu.Lock()
						defer l.mu.Unlock()
						l.committed = append(l.committed, title)
					})

					return nil
				}),
		}
	}
}

func TestAdminAfterSaveVirtualFieldInTransaction(t *testing.T) {
	t.Parallel()

	var log saveLog

	app := newApp(t, options{resource: log.resource(false)})
	createPost(t, app, Post{Title: "First", Status: "draft"})

	c := as(t, app, "admin")

	// The virtual field renders from its value function.
	c.Get("/admin/posts/1/edit").AssertOK().AssertSee(`name="pin"`)

	c.PostForm("/admin/posts/1", url.Values{"title": {"Renamed"}, "status": {"draft"}, "pin": {"false", "true"}}).
		AssertRedirect("/admin/posts/1")
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "Renamed", "status": "pinned"})

	log.mu.Lock()
	saves, committed := log.saves, log.committed
	log.mu.Unlock()

	if len(saves) != 1 || saves[0].Action != admin.ActionUpdate || saves[0].Before == nil ||
		saves[0].Before.Title != "First" || saves[0].Virtual["pin"] != true {
		t.Fatalf("AfterSave saw %+v", saves)
	}

	if len(committed) != 1 || committed[0] != "Renamed" {
		t.Fatalf("AfterCommit ran %v, want [Renamed]", committed)
	}

	logs := auditLogs(t, app)
	if len(logs) != 1 || logs[0].Changes.V["status"].After != "pinned" {
		t.Fatalf("audit = %+v, want the hook's status change", logs)
	}

	c.Get("/admin/posts/1/edit").AssertOK().AssertSee(`checked`)

	c.PostForm("/admin/posts", url.Values{"title": {"Second"}, "status": {"live"}}).AssertRedirect("/admin/posts/2")

	log.mu.Lock()
	defer log.mu.Unlock()

	if len(log.saves) != 2 || log.saves[1].Action != admin.ActionCreate || log.saves[1].Before != nil ||
		len(log.saves[1].Virtual) != 0 {
		t.Fatalf("create AfterSave saw %+v", log.saves[1])
	}
}

func TestAdminAfterSaveErrorRollsBack(t *testing.T) {
	t.Parallel()

	var log saveLog

	app := newApp(t, options{resource: log.resource(true)})
	createPost(t, app, Post{Title: "First", Status: "draft"})

	c := as(t, app, "admin")
	c.PostForm("/admin/posts/1", url.Values{"title": {"Renamed"}, "status": {"draft"}, "pin": {"true"}}).
		AssertStatus(500)

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "First", "status": "draft"})

	if logs := auditLogs(t, app); len(logs) != 0 {
		t.Fatalf("audit after a failed hook = %+v", logs)
	}

	log.mu.Lock()
	defer log.mu.Unlock()

	if len(log.committed) != 0 {
		t.Fatalf("AfterCommit ran after a rollback: %v", log.committed)
	}
}

func TestAdminVirtualFieldErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]admin.AnyResource{
		"undeclared virtual": admin.NewResource[Post](pinSchema),
		"virtual is a column": admin.NewResource[Post](pinSchema).
			Virtual("pin", nil).Virtual("title", nil),
		"virtual not in schema": admin.NewResource[Post](pinSchema).
			Virtual("pin", nil).Virtual("nope", nil),
	}

	for name, res := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := newPanel(t).Register(res); err == nil {
				t.Errorf("Register(%s) = nil error", name)
			}
		})
	}

	if err := newPanel(t).Register(admin.NewResource[Post](pinSchema).Virtual("pin", nil)); err != nil {
		t.Errorf("Register(declared virtual) = %v", err)
	}
}
