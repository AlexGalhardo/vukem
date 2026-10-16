package policies

import (
	"example.com/blog/app/models"
	"github.com/AlexGalhardo/vukem/authz"
)

// All lists the policies registered on the gate; make:policy appends here.
func All() []authz.Policy {
	return []authz.Policy{
		authz.For[*models.Post](PostPolicy{}),
	}
}
