package turso_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/drivers/turso"
)

// A local file by default; with TURSO_DATABASE_URL and TURSO_AUTH_TOKEN set
// it also syncs with Turso Cloud.
func ExampleNew() {
	opts, err := turso.OptionsFromEnv()
	if err != nil {
		fmt.Println(err)
		return
	}

	db := turso.New(opts)
	drv := db.Driver() // vukem.WithDatabaseDrivers(drv), DATABASE_URL=turso://storage/database.db

	fmt.Println(drv.Schemes, turso.Dialect.Name())
	// Output: [turso] turso
}
