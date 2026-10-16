package orm_test

import (
	"bytes"
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
)

type Patient struct {
	orm.Model
	Name      string
	Diagnosis orm.Encrypted[string] `json:"-"`
}

func ExampleEncrypted() {
	ctx := context.Background()

	// Apps pass app.Crypt; app.DB() does it for you.
	enc, _ := crypt.New(bytes.Repeat([]byte{1}, crypt.KeySize))

	db, _ := database.Open(ctx, ":memory:", database.WithEncrypter(enc))
	defer db.Close()

	_, _ = db.ExecContext(ctx, `CREATE TABLE patients (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT, diagnosis TEXT)`)

	p := Patient{Name: "Ada", Diagnosis: orm.Encrypted[string]{V: "flu"}}
	if err := orm.Create(ctx, db, &p); err != nil {
		fmt.Println(err)
		return
	}

	var stored string
	_ = db.QueryRowContext(ctx, "SELECT diagnosis FROM patients").Scan(&stored)
	fmt.Println("stored plaintext:", stored == "flu" || stored == `"flu"`)

	got, _ := orm.QueryOf[Patient](db).Find(ctx, p.ID)
	fmt.Println("decrypted:", got.Diagnosis.V)
	// Output:
	// stored plaintext: false
	// decrypted: flu
}
