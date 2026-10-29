package orm

import (
	"testing"
)

// scored maps a stored generated column: the database computes Overall,
// so the ORM must never write it.
type scored struct {
	ID      int64
	Story   int
	Art     int
	Overall float64 `orm:"readonly"`
}

func TestReadonlyColumnIsReadButNeverWritten(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE scoreds (id INTEGER PRIMARY KEY AUTOINCREMENT,
		story INTEGER NOT NULL, art INTEGER NOT NULL,
		overall REAL GENERATED ALWAYS AS ((story + art) / 2.0) STORED)`); err != nil {
		t.Fatal(err)
	}

	s := scored{Story: 4, Art: 2, Overall: 99}
	if err := Create(ctx, db, &s); err != nil {
		t.Fatalf("Create = %v, want nil (overall must not be inserted)", err)
	}

	s.Art = 4
	if err := Update(ctx, db, &s); err != nil {
		t.Fatalf("Update = %v, want nil (overall must not be updated)", err)
	}

	more := []scored{{Story: 1, Art: 1}, {Story: 2, Art: 4}}
	if err := CreateMany(ctx, db, more); err != nil {
		t.Fatalf("CreateMany = %v", err)
	}

	got, err := QueryOf[scored](db).Find(ctx, s.ID)
	if err != nil || got.Overall != 4 {
		t.Fatalf("Find = %+v, %v, want Overall 4", got, err)
	}

	if cols := Columns[scored](); len(cols) != 4 {
		t.Errorf("Columns = %v, want the readonly column listed for reads", cols)
	}
}

type badColumnTag struct {
	ID   int64
	Name string `orm:"readonyl"`
}

func TestUnknownColumnOrmTagPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("metaOf accepted an unknown orm tag option on a column")
		}
	}()

	metaOf[badColumnTag]()
}
