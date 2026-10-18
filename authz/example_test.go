package authz_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/authz"
)

type exUser struct {
	ID    int
	Admin bool
}

type exPost struct{ AuthorID int }

type exPostPolicy struct{}

func (exPostPolicy) Update(_ context.Context, u *exUser, p *exPost) bool {
	return u.ID == p.AuthorID
}

func Example() {
	// In an app the resolver reads auth.UserFrom[models.User](ctx, "web")
	// (the session guard stores the user by value) and returns &u.
	gate := authz.New(func(context.Context) (*exUser, error) { return &exUser{ID: 1}, nil })
	gate.Define("edit-settings", func(_ context.Context, u *exUser) bool { return u.Admin })
	if err := gate.Register(authz.For[*exPost](exPostPolicy{})); err != nil {
		fmt.Println(err)
		return
	}

	ctx := context.Background()
	alice := &exUser{ID: 1}
	fmt.Println(gate.Allows(ctx, alice, "update", &exPost{AuthorID: 1}))
	fmt.Println(gate.Allows(ctx, alice, "update", &exPost{AuthorID: 2}))
	fmt.Println(gate.Allows(ctx, alice, "edit-settings", nil))
	fmt.Println(gate.Allows(ctx, alice, "never-defined", nil))
	// Output:
	// true
	// false
	// false
	// false
}
