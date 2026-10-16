// Package factories builds models with fake data for tests and seeders.
package factories

import (
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/factory"
)

// User builds models.User; the password is a placeholder hash, so seeded
// users sign in through password reset.
var User = factory.New(func(f *factory.Faker) models.User {
	return models.User{
		Name:     f.Name(),
		Email:    f.Email(),
		Password: "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
	}
})

// Post builds models.Post; set UserID with State.
var Post = factory.New(func(f *factory.Faker) models.Post {
	return models.Post{Title: f.Words(4), Body: f.Paragraph()}
})
