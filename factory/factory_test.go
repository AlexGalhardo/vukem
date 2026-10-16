package factory

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
)

type User struct {
	orm.Model

	Name  string
	Email string
	Role  string
	Posts orm.HasMany[Post]
}

type Post struct {
	orm.Model

	UserID int64
	Title  string
}

var users = New(func(f *Faker) User {
	return User{Name: f.Name(), Email: f.Email(), Role: "member"}
})

var posts = New(func(f *Faker) Post { return Post{Title: f.Sentence()} })

func setup(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, ddl := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP,
			name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, role TEXT NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP,
			user_id INTEGER NOT NULL REFERENCES users(id), title TEXT NOT NULL)`,
	} {
		if _, err := db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}

	return db
}

func TestMakeStatesAndSeed(t *testing.T) {
	t.Parallel()

	admins := users.Count(3).State(func(u *User) { u.Role = "admin" }).Make()
	if len(admins) != 3 || admins[0].Role != "admin" || admins[0].ID != 0 {
		t.Fatalf("Make = %+v", admins)
	}

	emails := map[string]bool{}
	for _, u := range admins {
		if _, err := mail.ParseAddress(u.Email); err != nil {
			t.Fatalf("invalid email %q", u.Email)
		}

		emails[u.Email] = true
	}

	if len(emails) != 3 {
		t.Fatal("emails are not unique")
	}

	a := users.Seed(42).Count(5).Make()
	b := users.Seed(42).Count(5).Make()

	if !slices.EqualFunc(a, b, func(x, y User) bool { return x.Name == y.Name && x.Email == y.Email }) {
		t.Fatal("same seed gave different data")
	}

	if users.Count(-1).Make() == nil || len(users.Count(0).Make()) != 0 {
		t.Fatal("Count bounds")
	}

	// States chain in order and do not leak into the base factory.
	base := users.State(func(u *User) { u.Role = "a" })
	_ = base.State(func(u *User) { u.Role += "b" })

	if got := base.MakeOne().Role; got != "a" {
		t.Fatalf("state leaked: %q", got)
	}
}

func TestCreateWithRelations(t *testing.T) {
	t.Parallel()

	db := setup(t)
	ctx := t.Context()

	withPosts := users.AfterCreate(func(ctx context.Context, ex database.Executor, u *User) error {
		_, err := posts.Count(2).State(func(p *Post) { p.UserID = u.ID }).Create(ctx, ex)
		return err
	})

	created, err := withPosts.Count(2).Create(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	if created[0].ID == 0 || created[1].ID == 0 {
		t.Fatalf("ids not set: %+v", created)
	}

	loaded, err := orm.QueryOf[User](db).With("Posts").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range loaded {
		if ps, _ := u.Posts.Get(); len(ps) != 2 {
			t.Fatalf("user %d has %d posts", u.ID, len(ps))
		}
	}

	one, err := users.CreateOne(ctx, db)
	if err != nil || one.ID == 0 {
		t.Fatalf("CreateOne = %+v %v", one, err)
	}

	// A failing hook rolls everything back.
	boom := errors.New("boom")
	failing := users.AfterCreate(func(context.Context, database.Executor, *User) error { return boom })

	if _, err := failing.Count(3).Create(ctx, db); !errors.Is(err, boom) {
		t.Fatalf("hook error = %v", err)
	}

	if n, _ := orm.QueryOf[User](db).Count(ctx); n != 3 {
		t.Fatalf("users after rollback = %d, want 3", n)
	}

	dup := users.State(func(u *User) { u.Email = "same@example.com" })
	if _, err := dup.Count(2).Create(ctx, db); !errors.Is(err, orm.ErrConflict) {
		t.Fatalf("duplicate = %v", err)
	}

	if _, err := dup.CreateOne(ctx, db); err != nil {
		t.Fatal(err)
	}

	if _, err := dup.CreateOne(ctx, db); err == nil {
		t.Fatal("CreateOne duplicate accepted")
	}
}

func TestFaker(t *testing.T) {
	t.Parallel()

	f := NewFaker(7)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	for range 200 {
		if n := f.Int(3, 5); n < 3 || n > 5 {
			t.Fatalf("Int = %d", n)
		}

		if x := f.Float(1, 2); x < 1 || x >= 2 {
			t.Fatalf("Float = %v", x)
		}

		if tm := f.Time(from, to); tm.Before(from) || !tm.Before(to) {
			t.Fatalf("Time = %v", tm)
		}
	}

	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	slug := regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+-\d+$`)

	if !uuid.MatchString(f.UUID()) || !slug.MatchString(f.Slug()) || f.Int(5, 5) != 5 || !f.Time(to, from).Equal(to) {
		t.Fatal("UUID/Slug/edge cases")
	}

	if s := f.Sentence(); s[len(s)-1] != '.' || f.Paragraph() == "" || f.Username() == "" || f.URL()[:8] != "https://" {
		t.Fatal("text helpers")
	}

	_ = f.Bool()

	if got := ascii("joão çã"); got != "joao ca" {
		t.Fatalf("ascii = %q", got)
	}
}
