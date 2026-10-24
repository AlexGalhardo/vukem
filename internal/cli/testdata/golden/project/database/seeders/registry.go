// Package seeders fills the database with demo data: vukem db:seed.
package seeders

import vukem "github.com/AlexGalhardo/vukem"

// All lists the seeders run by vukem db:seed, in order; make:seeder appends here.
func All() []vukem.Seeder {
	return []vukem.Seeder{
		UsersSeeder{},
	}
}

// AllApp lists the seeders that receive the booted App, run by vukem db:seed
// in option order; make:seeder --app appends here. Register it with
// vukem.WithAppSeeders(seeders.AllApp()...).
func AllApp() []vukem.AppSeeder {
	return []vukem.AppSeeder{
		ReportsSeeder{},
	}
}
