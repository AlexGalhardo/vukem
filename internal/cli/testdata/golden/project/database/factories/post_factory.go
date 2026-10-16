package factories

import (
	"github.com/AlexGalhardo/vukem/factory"

	"example.com/blog/app/models"
)

// Post builds models.Post with fake data:
// factories.Post.Count(10).Create(ctx, db).
var Post = factory.New(func(f *factory.Faker) models.Post {
	return models.Post{}
})
