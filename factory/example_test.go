package factory_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/factory"
	"github.com/AlexGalhardo/vukem/orm"
)

type User struct {
	orm.Model
	Name string
	Role string
}

func ExampleFactory_Make() {
	users := factory.New(func(f *factory.Faker) User {
		return User{Name: f.Name(), Role: "member"}
	})

	admins := users.Count(2).State(func(u *User) { u.Role = "admin" }).Make()
	for _, u := range admins {
		fmt.Println(u.Role, u.Name != "")
	}
	// Output:
	// admin true
	// admin true
}

func ExampleFactory_Create() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	_, _ = db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT, role TEXT)`)

	users := factory.New(func(f *factory.Faker) User {
		return User{Name: f.Name(), Role: "member"}
	})

	created, err := users.Count(3).Create(ctx, db)
	if err != nil {
		fmt.Println(err)
		return
	}

	n, _ := orm.QueryOf[User](db).Count(ctx)
	fmt.Println(len(created), n, created[0].ID > 0)
	// Output: 3 3 true
}

func ExampleNewFakerWithLocale() {
	f := factory.NewFakerWithLocale(2026, factory.LocalePTBR())

	fmt.Println(f.Name())
	fmt.Println(f.ProductTitle())
	fmt.Println(f.Category())
	fmt.Println(f.Address())
	// Output:
	// Arthur Dias
	// Mochila Rústica de Cerâmica
	// Beleza
	// Avenida Getúlio Vargas, 748 - Manaus/AM, 37267-205
}
