package seeders

import (
	"context"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/examples/blog/database/factories"
)

// PostsSeeder creates two authors with five posts each.
type PostsSeeder struct{}

// Seed implements vukem.Seeder.
func (PostsSeeder) Seed(ctx context.Context, db *database.DB) error {
	authors, err := factories.User.Count(2).Create(ctx, db)
	if err != nil {
		return err
	}

	for _, a := range authors {
		if _, err := factories.Post.Count(5).
			State(func(p *models.Post) { p.UserID = a.ID }).
			Create(ctx, db); err != nil {
			return err
		}
	}

	return nil
}
