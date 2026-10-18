package authz

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/web"
)

type User struct {
	ID    int64
	Admin bool
}

type Post struct {
	ID     int64
	UserID int64
	Draft  bool
}

type Comment struct{ ID int64 }

type PostPolicy struct{}

func (PostPolicy) View(_ context.Context, u *User, p *Post) bool { return !p.Draft || u.ID == p.UserID }

func (PostPolicy) Update(_ context.Context, u *User, p *Post) Result {
	if u.ID == p.UserID {
		return Allow()
	}

	if p.Draft {
		return DenyAsNotFound()
	}

	return Deny("You do not own this post.")
}

func (PostPolicy) Create(_ context.Context, u *User) bool { return u.ID > 0 }

// Helper returns a label; methods with other signatures are ignored.
func (PostPolicy) Helper() string { return "post" }

type userKey struct{}

func gate() *Gate[*User] {
	g := New(func(ctx context.Context) (*User, error) {
		if u, ok := ctx.Value(userKey{}).(*User); ok {
			return u, nil
		}

		return nil, errors.New("no user")
	})

	g.Define("edit-settings", func(_ context.Context, u *User) bool { return u.Admin })
	g.DefineResult("see-secret", func(context.Context, *User) Result { return DenyAsNotFound() })

	if err := g.Register(For[*Post](PostPolicy{})); err != nil {
		panic(err)
	}

	return g
}

func as(u *User) context.Context { return context.WithValue(context.Background(), userKey{}, u) }

func TestGatesAndPolicies(t *testing.T) {
	t.Parallel()

	g := gate()
	ana, bob, admin := &User{ID: 1}, &User{ID: 2}, &User{ID: 3, Admin: true}
	post := &Post{ID: 10, UserID: 1}
	draft := &Post{ID: 11, UserID: 1, Draft: true}

	tests := []struct {
		user    *User
		ability string
		subject any
		status  int // 0 = allowed
	}{
		{admin, "edit-settings", nil, 0},
		{ana, "edit-settings", nil, 403},
		{ana, "see-secret", nil, 404},
		{ana, "update", post, 0},
		{bob, "update", post, 403},
		{bob, "update", draft, 404},
		{bob, "view", post, 0},
		{bob, "view", draft, 403},
		{ana, "view", draft, 0},
		{ana, "create", (*Post)(nil), 0},
		{&User{}, "create", (*Post)(nil), 403},
	}

	for _, tt := range tests {
		err := g.Authorize(as(tt.user), tt.ability, tt.subject)
		if got := statusOf(err); got != tt.status {
			t.Errorf("user %d %s %T = %d (%v), want %d", tt.user.ID, tt.ability, tt.subject, got, err, tt.status)
		}

		if g.Allows(context.Background(), tt.user, tt.ability, tt.subject) != (tt.status == 0) {
			t.Errorf("Allows disagrees for %s", tt.ability)
		}
	}

	if err := g.Authorize(as(bob), "update", post); !strings.Contains(err.Error(), "You do not own this post.") {
		t.Errorf("custom message lost: %v", err)
	}

	if err := g.Authorize(context.Background(), "update", post); !errors.Is(err, ErrNoUser) {
		t.Errorf("no user = %v", err)
	}

	// Before hooks decide first.
	g.Before(func(_ context.Context, u *User, _ string) *Result {
		if u.Admin {
			r := Allow()
			return &r
		}

		return nil
	})

	if err := g.Authorize(as(admin), "update", post); err != nil {
		t.Errorf("admin before hook = %v", err)
	}

	if err := g.Authorize(as(bob), "update", post); err == nil {
		t.Error("before hook allowed a non-admin")
	}
}

func statusOf(err error) int {
	if err == nil {
		return 0
	}

	return errs.Status(err)
}

func TestSecurity_A01_UnregisteredPolicyDenies(t *testing.T) {
	t.Parallel()

	g := gate()
	ana := &User{ID: 1}

	for name, check := range map[string]func() error{
		"unknown ability":       func() error { return g.Authorize(as(ana), "launch-missiles", nil) },
		"unregistered policy":   func() error { return g.Authorize(as(ana), "update", &Comment{ID: 1}) },
		"unknown policy method": func() error { return g.Authorize(as(ana), "archive", &Post{UserID: 1}) },
		"unregistered value":    func() error { return g.Authorize(as(ana), "update", Comment{ID: 1}) },
		"value, unknown method": func() error { return g.Authorize(as(ana), "archive", Post{UserID: 1}) },
	} {
		err := check()
		if statusOf(err) != 403 || !errors.Is(err, errUnregistered) {
			t.Errorf("%s = %v, want a 403 caused by errUnregistered", name, err)
		}
	}

	if g.Allows(context.Background(), ana, "launch-missiles", nil) {
		t.Fatal("Allows granted an unregistered ability")
	}
}

type badPolicy struct{}

func (badPolicy) Update(context.Context, *User, *Comment) bool { return true }

type emptyPolicy struct{}

func TestRegisterValidatesSignatures(t *testing.T) {
	t.Parallel()

	g := gate()
	if err := g.Register(
		For[*Post](badPolicy{}),
	); err == nil ||
		!strings.Contains(err.Error(), "takes *authz.Comment") {
		t.Fatalf("wrong subject type = %v", err)
	}

	if err := g.Register(For[*Post](emptyPolicy{})); err == nil {
		t.Fatal("policy without methods accepted")
	}
}

func TestCtxAuthorizeAndTemplateCan(t *testing.T) {
	t.Parallel()

	g := gate()
	post := &Post{ID: 10, UserID: 1}

	var logs bytes.Buffer

	s := &web.Services{Authorizer: g, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	req := httptest.NewRequestWithContext(as(&User{ID: 2}), http.MethodGet, "/posts/10", http.NoBody)
	req = req.WithContext(web.WithServices(req.Context(), s))
	c := web.NewCtx(httptest.NewRecorder(), req, s)

	if err := c.Authorize("update", post); errs.Status(err) != 403 || !strings.Contains(logs.String(), "authz.denied") {
		t.Fatalf("Ctx.Authorize = %v, logs %q", err, logs.String())
	}

	if err := c.Authorize("view", post); err != nil {
		t.Fatalf("view = %v", err)
	}

	// No Authorizer configured: deny.
	bare := web.NewCtx(
		httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody),
		nil,
	)
	if err := bare.Authorize("anything", nil); errs.Status(err) != 403 {
		t.Fatalf("no authorizer = %v", err)
	}

	engine, err := view.New(view.Config{FS: fstest.MapFS{"p.html": {Data: []byte(
		`{{if can "update" .}}edit{{else}}read-only{{end}}|{{if can "edit-settings"}}settings{{end}}`)}}})
	if err != nil {
		t.Fatal(err)
	}

	render := func(ctx context.Context) string {
		var b bytes.Buffer
		if err := engine.Render(ctx, &b, "p", post); err != nil {
			t.Fatal(err)
		}

		return b.String()
	}

	owner := web.WithServices(as(&User{ID: 1}), s)
	admin := web.WithServices(as(&User{ID: 3, Admin: true}), s)

	if got := render(owner); got != "edit|" {
		t.Fatalf("owner = %q", got)
	}

	if got := render(admin); got != "read-only|settings" {
		t.Fatalf("admin = %q", got)
	}

	if got := render(context.Background()); got != "read-only|" {
		t.Fatalf("no services = %q", got)
	}
}

// A value subject (a range over []Post in a template) resolves the policy
// registered for *Post with a copy, so "can" works in loops.
func TestValueSubjectUsesPointerPolicy(t *testing.T) {
	t.Parallel()

	g := gate()
	ctx := as(&User{ID: 1})

	tests := []struct {
		name    string
		ability string
		subject any
		want    bool
	}{
		{"value owner", "update", Post{ID: 1, UserID: 1}, true},
		{"value other", "update", Post{ID: 2, UserID: 9}, false},
		{"pointer owner", "update", &Post{ID: 1, UserID: 1}, true},
		{"value without policy", "update", Comment{ID: 1}, false},
		{"value unknown ability", "publish", Post{ID: 1, UserID: 1}, false},
	}

	for _, tt := range tests {
		if got := g.Allows(ctx, &User{ID: 1}, tt.ability, tt.subject); got != tt.want {
			t.Errorf("%s: Allows = %v, want %v", tt.name, got, tt.want)
		}

		if got := g.Authorize(ctx, tt.ability, tt.subject) == nil; got != tt.want {
			t.Errorf("%s: Authorize allowed = %v, want %v", tt.name, got, tt.want)
		}
	}

	engine, err := view.New(view.Config{FS: fstest.MapFS{"list.html": {Data: []byte(
		`{{range .}}{{.ID}}:{{if can "update" .}}edit{{else}}-{{end}} {{end}}`)}}})
	if err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer

	s := &web.Services{Authorizer: g}
	if err := engine.Render(
		web.WithServices(ctx, s),
		&b,
		"list",
		[]Post{{ID: 1, UserID: 1}, {ID: 2, UserID: 9}},
	); err != nil {
		t.Fatal(err)
	}

	if got := b.String(); got != "1:edit 2:- " {
		t.Errorf("range over []Post = %q, want %q", got, "1:edit 2:- ")
	}
}
