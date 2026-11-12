package tenancy_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/tenancy"
)

func ExampleScope() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	_, _ = db.ExecContext(ctx, `CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, workspace_id TEXT NOT NULL, title TEXT)`)

	// Task.BeforeCreate calls tenancy.Assign, so the tenant comes from ctx.
	_ = orm.Create(tenancy.With(ctx, "acme"), db, &Task{Title: "send invoice"})
	_ = orm.Create(tenancy.With(ctx, "globex"), db, &Task{Title: "plan launch"})

	acme := tenancy.With(ctx, "acme")
	rows, _ := orm.QueryOf[Task](db).Scope(tenancy.Scope[Task](acme, "workspace_id")).Get(acme)
	fmt.Println(len(rows), rows[0].Title)

	// No tenant in the context: the scope matches nothing.
	rows, _ = orm.QueryOf[Task](db).Scope(tenancy.Scope[Task](ctx, "workspace_id")).Get(ctx)
	fmt.Println(len(rows))
	// Output:
	// 1 send invoice
	// 0
}
