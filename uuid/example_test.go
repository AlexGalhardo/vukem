package uuid_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/uuid"
)

type Order struct {
	orm.Model
	PublicID uuid.UUID
	Parent   *uuid.UUID
}

func Example() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	_, _ = db.ExecContext(ctx, `CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, public_id TEXT UNIQUE, parent TEXT)`)

	o := Order{PublicID: uuid.NewV7()}
	if err := orm.Create(ctx, db, &o); err != nil {
		fmt.Println(err)
		return
	}

	got, err := orm.QueryOf[Order](db).Where("public_id", o.PublicID).First(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(got.PublicID == o.PublicID, got.PublicID.Version(), got.Parent == nil)

	_, err = uuid.Parse("not-a-uuid")
	fmt.Println(err)
	// Output:
	// true 7 true
	// uuid: invalid UUID
}
