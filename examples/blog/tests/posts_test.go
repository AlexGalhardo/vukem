package tests

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestAuthorsWritePosts(t *testing.T) {
	app := newApp(t)
	ana := user(t, app, "ana@example.com")
	c := app.Client(t).ActingAs("web", ana)

	app.Client(t).Get("/posts/create").AssertRedirect("/login") // guests cannot write

	c.PostForm("/posts", url.Values{"title": {"Hello Vukem"}, "body": {"First post."}}).AssertSessionHas("success")
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello Vukem", "user_id": ana.ID})

	db, _ := app.DB()
	post, _ := orm.QueryOf[models.Post](db).Where("title", "Hello Vukem").First(t.Context())
	path := "/posts/" + strconv.FormatInt(post.ID, 10)

	c.Get(path).AssertOK().AssertSee("by ana@example.com").AssertSee("Edit")
	app.Client(t).Get(path).AssertOK().AssertDontSee(">Edit<")
	app.Client(t).Get("/posts").AssertOK().AssertSee("Hello Vukem")

	c.PostForm(path, url.Values{"_method": {"PUT"}, "title": {"Edited title"}, "body": {""}}).AssertRedirect(path)
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Edited title"})
}

func TestOnlyAuthorsChangePosts(t *testing.T) {
	app := newApp(t)
	ana, bob := user(t, app, "ana@example.com"), user(t, app, "bob@example.com")

	db, _ := app.DB()
	post := models.Post{UserID: ana.ID, Title: "Ana's post", Body: "x"}

	if err := orm.Create(t.Context(), db, &post); err != nil {
		t.Fatal(err)
	}

	path := "/posts/" + strconv.FormatInt(post.ID, 10)
	intruder := app.Client(t).ActingAs("web", bob)

	// The index ranges over []models.Post values; the *Post policy still decides.
	app.Client(t).ActingAs("web", ana).Get("/posts").AssertOK().AssertSee(`hx-delete="` + path + `"`)
	intruder.Get("/posts").AssertOK().AssertDontSee("hx-delete")

	intruder.Get(path + "/edit").AssertForbidden()
	intruder.PostForm(path, url.Values{"_method": {"PUT"}, "title": {"Hacked"}}).AssertForbidden()
	intruder.HTMX().Delete(path).AssertForbidden()
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Ana's post"})

	app.Client(t).ActingAs("web", ana).HTMX().Delete(path).AssertOK()
	vukemtest.AssertDatabaseCount(t, app, "posts", 0)
}

func TestCommentsAndEagerLoading(t *testing.T) {
	app := newApp(t)
	ana, bob := user(t, app, "ana@example.com"), user(t, app, "bob@example.com")

	db, _ := app.DB()
	post := models.Post{UserID: ana.ID, Title: "Discuss", Body: "x"}

	if err := orm.Create(t.Context(), db, &post); err != nil {
		t.Fatal(err)
	}

	path := "/posts/" + strconv.FormatInt(post.ID, 10)

	app.Client(t).
		ActingAs("web", bob).
		PostForm(path+"/comments", url.Values{"body": {"Nice post!"}}).
		AssertRedirect(path)
	app.Client(t).PostForm(path+"/comments", url.Values{"body": {"Anonymous"}}).AssertRedirect("/login")

	app.Client(t).Get(path).AssertOK().AssertSee("Nice post!").AssertDontSee("Anonymous")

	app.Client(t).GetJSON(path).AssertOK().
		AssertJSONPath("author.email", "ana@example.com").
		AssertJSONCount("comments", 1)
}

func TestSeeder(t *testing.T) {
	app := newApp(t)

	if err := app.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}

	vukemtest.AssertDatabaseCount(t, app, "users", 2)
	vukemtest.AssertDatabaseCount(t, app, "posts", 10)
	app.Client(t).GetJSON("/posts").AssertOK().AssertJSONCount("data", 10)
}
