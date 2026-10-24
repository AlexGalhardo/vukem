package seeders

import (
	"context"

	vukem "github.com/AlexGalhardo/vukem"
)

// ReportsSeeder seeds data that needs the booted app (configuration,
// logger, storage disks); vukem db:seed runs it after the database seeders.
type ReportsSeeder struct{}

// SeedApp implements vukem.AppSeeder. Open the database with app.DB() when
// the seeder needs it.
func (ReportsSeeder) SeedApp(_ context.Context, _ *vukem.App) error {
	return nil
}
