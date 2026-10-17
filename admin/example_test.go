package admin_test

import (
	"context"
	"strings"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/router"
)

// The panel needs a database, a guard and an authorizer granting the
// "admin" ability plus a policy per model.
func ExampleNew() {
	mount := func(r *router.Router, db *database.DB, guard *auth.SessionGuard[User]) error {
		gate := authz.New(func(ctx context.Context) (*User, error) {
			u, err := auth.UserFrom[User](ctx, guard.Name())
			return &u, err
		})
		gate.Define(admin.AbilityAdmin, func(_ context.Context, u *User) bool { return u.Role == "admin" })

		if err := gate.Register(authz.For[*Post](adminPolicy[*Post]{})); err != nil {
			return err
		}

		panel, err := admin.New(admin.Config{DB: db, Guard: guard, Authorizer: gate})
		if err != nil {
			return err
		}

		posts := admin.NewResource[Post](postSchema).
			Search("title").
			Filter("status", "draft", "live").
			Column("Excerpt", func(p Post) string { return strings.SplitN(p.Body, "\n", 2)[0] })

		if err := panel.Register(posts); err != nil {
			return err
		}

		return panel.Mount(r) // GET /admin, /admin/posts, ...
	}

	_ = mount
}
