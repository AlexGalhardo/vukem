package policies

import (
	"github.com/AlexGalhardo/vukem/authz"

	"example.com/blog/app/models"
)

// All lists the policies registered on the gate; make:policy appends here.
func All() []authz.Policy {
	return []authz.Policy{
		authz.For[*models.Post](PostPolicy{}),
	}
}
