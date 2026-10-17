package orm

import (
	"fmt"
	"testing"
)

func seedN(b *testing.B, n int) Query[User] {
	b.Helper()

	db, ctx := setup(b)
	for i := range n {
		u := User{Name: fmt.Sprintf("User %d", i), Email: fmt.Sprintf("u%d@x.io", i), Age: i}
		if err := Create(ctx, db, &u); err != nil {
			b.Fatal(err)
		}
	}

	return QueryOf[User](db)
}

func BenchmarkQueryFind(b *testing.B) {
	q := seedN(b, 10)
	ctx := b.Context()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := q.Find(ctx, 5); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryGet100(b *testing.B) {
	q := seedN(b, 100).OrderBy("id", Asc)
	ctx := b.Context()

	b.ReportAllocs()

	for b.Loop() {
		if got, err := q.Get(ctx); err != nil || len(got) != 100 {
			b.Fatal(len(got), err)
		}
	}
}
