package ulid_test

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/ulid"
)

// Conversion keys its rows by a ULID instead of embedding orm.Model, so the
// key can be handed to clients without leaking how many rows the table
// holds and still orders newest-last without a second column.
type Conversion struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func Example() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	_, _ = db.ExecContext(ctx, `CREATE TABLE conversions (id CHAR(26) PRIMARY KEY,
		name TEXT, created_at TIMESTAMP, updated_at TIMESTAMP)`)

	// One generator per app, wired like any other service: it owns the
	// state that keeps ids made in the same millisecond in order.
	ids := ulid.NewGenerator()

	// orm.Create inserts a string key exactly as given, so the caller sets
	// it; only an integer key comes back from the database.
	c := Conversion{ID: ids.New(), Name: "report.docx"}
	if err := orm.Create(ctx, db, &c); err != nil {
		fmt.Println(err)
		return
	}

	got, err := orm.QueryOf[Conversion](db).Find(ctx, c.ID)
	if err != nil {
		fmt.Println(err)
		return
	}

	at, ok := ulid.TimeOf(got.ID)

	fmt.Println(got.Name, len(got.ID), ulid.Valid(got.ID), ok, !at.After(got.CreatedAt))
	fmt.Println(ulid.Valid("not-an-id"))
	// Output:
	// report.docx 26 true true true
	// false
}
