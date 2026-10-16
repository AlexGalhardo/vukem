package seeders

import (
	"context"

	"github.com/AlexGalhardo/vukem/database"
)

// UsersSeeder fills the database; vukem db:seed runs it.
type UsersSeeder struct{}

// Seed implements vukem.Seeder.
func (UsersSeeder) Seed(ctx context.Context, db *database.DB) error {
	return nil
}
