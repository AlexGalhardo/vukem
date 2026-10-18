package seeders

import (
	"context"

	"github.com/AlexGalhardo/vukem/database"
)

// UsersSeeder fills the database; vukem db:seed runs it.
type UsersSeeder struct{}

// Seed implements vukem.Seeder. Name the parameters (ctx, db) with the
// first row it inserts.
func (UsersSeeder) Seed(_ context.Context, _ *database.DB) error {
	return nil
}
