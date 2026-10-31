package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// racingWriter changes a post column once, as another session would between
// the panel loading the row and saving it.
func racingWriter(col string, val any) func(context.Context, *database.DB) {
	done := false

	return func(ctx context.Context, db *database.DB) {
		if done {
			return
		}

		done = true

		if _, err := orm.QueryOf[Post](db).Where("id", 1).Update(ctx, orm.Set{col: val}); err != nil {
			panic(err)
		}
	}
}

func TestAdminUpdateKeepsConcurrentChangeToOtherColumn(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{racing: racingWriter("body", "written elsewhere")})
	createPost(t, app, Post{Title: "First", Body: "original", Status: "draft"})

	as(
		t,
		app,
		"admin",
	).PostForm("/admin/posts/1", url.Values{"title": {"Renamed"}, "body": {"original"}, "status": {"draft"}}).
		AssertRedirect("/admin/posts/1")

	vukemtest.AssertDatabaseHas(
		t,
		app,
		"posts",
		map[string]any{"id": 1, "title": "Renamed", "body": "written elsewhere"},
	)
}

func TestAdminUpdateConflictOnSameColumn(t *testing.T) {
	t.Parallel()

	app := newApp(t, options{racing: racingWriter("title", "Other title")})
	createPost(t, app, Post{Title: "First", Body: "original", Status: "draft"})

	as(
		t,
		app,
		"admin",
	).PostForm("/admin/posts/1", url.Values{"title": {"Renamed"}, "body": {"original"}, "status": {"draft"}}).
		AssertStatus(http.StatusUnprocessableEntity).
		AssertSee("conflicts")

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"id": 1, "title": "Other title"})
}
