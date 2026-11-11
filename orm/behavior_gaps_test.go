package orm

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
)

type gapOwner struct {
	ID   int64 `db:"id"`
	Name string

	Pet  HasOne[gapPet]     `orm:"fk:owner_id"`
	Tags ManyToMany[gapTag] `orm:"pivot:gap_owner_tag,fk:owner_id,rk:tag_id"`
}

func (gapOwner) Table() string { return "gap_owners" }

type gapPet struct {
	ID      int64 `db:"id"`
	OwnerID int64
	Name    string

	Toys HasMany[gapToy] `orm:"fk:pet_id"`
}

func (gapPet) Table() string { return "gap_pets" }

type gapToy struct {
	ID    int64 `db:"id"`
	PetID int64
	Name  string
}

func (gapToy) Table() string { return "gap_toys" }

type gapTag struct {
	ID      int64 `db:"id"`
	Label   string
	BadgeID int64

	Badge BelongsTo[gapBadge] `orm:"fk:badge_id"`
}

func (gapTag) Table() string { return "gap_tags" }

type gapBadge struct {
	ID    int64 `db:"id"`
	Color string
}

func (gapBadge) Table() string { return "gap_badges" }

const gapDDL = `
CREATE TABLE gap_owners (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE gap_pets (id INTEGER PRIMARY KEY, owner_id INTEGER NOT NULL, name TEXT NOT NULL);
CREATE TABLE gap_toys (id INTEGER PRIMARY KEY, pet_id INTEGER NOT NULL, name TEXT NOT NULL);
CREATE TABLE gap_tags (id INTEGER PRIMARY KEY, label TEXT NOT NULL, badge_id INTEGER NOT NULL);
CREATE TABLE gap_badges (id INTEGER PRIMARY KEY, color TEXT NOT NULL);
CREATE TABLE gap_owner_tag (owner_id INTEGER NOT NULL, tag_id INTEGER NOT NULL);
INSERT INTO gap_owners VALUES (1, 'Ana'), (2, 'Bob');
INSERT INTO gap_pets VALUES (1, 1, 'Rex');
INSERT INTO gap_toys VALUES (1, 1, 'ball'), (2, 1, 'rope');
INSERT INTO gap_tags VALUES (1, 'vip', 1), (2, 'new', 2);
INSERT INTO gap_badges VALUES (1, 'gold'), (2, 'blue');
INSERT INTO gap_owner_tag VALUES (1, 1), (1, 2), (2, 2);
`

func gapSetup(t *testing.T) (*database.DB, context.Context) {
	t.Helper()

	db, ctx := setup(t)

	for stmt := range strings.SplitSeq(gapDDL, ";\n") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}

		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	return db, ctx
}

// A nested With reaches the models loaded by every kind of relation: the
// next level is loaded for the rows a HasOne, a ManyToMany, a HasMany and a
// BelongsTo just brought in.
func TestNestedWithThroughEveryRelationKind(t *testing.T) {
	t.Parallel()

	db, ctx := gapSetup(t)

	owners, err := QueryOf[gapOwner](db).With("Pet.Toys", "Tags.Badge").OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pet, err := owners[0].Pet.Get()
	if err != nil || pet == nil {
		t.Fatalf("Ana's pet = %v, %v", pet, err)
	}

	toys, err := pet.Toys.Get()
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, toy := range toys {
		names = append(names, toy.Name)
	}

	if diff := cmp.Diff([]string{"ball", "rope"}, names); diff != "" {
		t.Errorf("toys of Ana's pet (-want +got):\n%s", diff)
	}

	if bob, err := owners[1].Pet.Get(); err != nil || bob != nil {
		t.Errorf("Bob has no pet, got %v, %v", bob, err)
	}

	colors := map[string][]string{}

	for _, o := range owners {
		tags, err := o.Tags.Get()
		if err != nil {
			t.Fatal(err)
		}

		for _, tag := range tags {
			badge, err := tag.Badge.Get()
			if err != nil || badge == nil {
				t.Fatalf("badge of %q not loaded: %v, %v", tag.Label, badge, err)
			}

			colors[o.Name] = append(colors[o.Name], tag.Label+":"+badge.Color)
		}
	}

	want := map[string][]string{"Ana": {"vip:gold", "new:blue"}, "Bob": {"new:blue"}}
	if diff := cmp.Diff(want, colors); diff != "" {
		t.Errorf("tag badges (-want +got):\n%s", diff)
	}
}

// A left join keeps the rows that have nothing related; the inner join
// drops them.
func TestLeftJoinRelationKeepsRowsWithoutRelated(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	inner, err := QueryOf[qaAuthor](db).JoinRelation("Books").Distinct().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}

	left, err := QueryOf[qaAuthor](db).LeftJoinRelation("Books").Distinct().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Cy wrote no book.
	if inner != 2 || left != 3 {
		t.Errorf("authors with inner join = %d, with left join = %d; want 2 and 3", inner, left)
	}
}

func TestAggregatesOverNothingAndOverGroups(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	if avg, err := QueryOf[qaBook](db).Where("views", ">", 0).Avg(ctx, "views"); err != nil || avg != 160.0/3 {
		t.Errorf("Avg(views) = %v, %v; want 53.33", avg, err)
	}

	// The average of nothing is NULL, which is not 0.
	if _, err := QueryOf[qaBook](db).Where("views", ">", 100000).Avg(ctx, "views"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Avg over no rows = %v, want ErrNotFound", err)
	}

	if _, err := QueryOf[qaBook](db).GroupBy("author_id").Sum[int64](ctx, "views"); !errors.Is(err, errProjected) {
		t.Errorf("Sum over a grouped query = %v, want the use-Scan error", err)
	}

	if _, err := QueryOf[qaBook](db).Sum[int64](ctx, "no_such_column"); err == nil {
		t.Error("Sum over an unknown column = nil")
	}
}

func TestWriteErrorsAnswerConflict(t *testing.T) {
	t.Parallel()

	if got := ErrStale.Error(); !strings.Contains(got, "reload it") {
		t.Errorf("ErrStale message = %q", got)
	}

	wrapped := errors.Join(errors.New("x"), ErrStale)
	if !errors.Is(wrapped, ErrStale) || errors.Is(wrapped, ErrConflict) {
		t.Error("ErrStale and ErrConflict must be distinct sentinels")
	}

	cause := errors.New("column code is not unique")
	conflict := &conflictError{cause: cause}

	if !errors.Is(conflict, ErrConflict) || !errors.Is(conflict, cause) {
		t.Errorf("a conflict must match ErrConflict and its cause: %v", conflict)
	}

	if msg := conflict.Error(); msg != "unique constraint violated: "+cause.Error() {
		t.Errorf("conflict message = %q", msg)
	}

	if msg := ErrConflict.Error(); msg != "unique constraint violated" {
		t.Errorf("bare conflict message = %q", msg)
	}

	for name, err := range map[string]error{"stale": ErrStale, "conflict": conflict} {
		if got := errs.Status(err); got != 409 {
			t.Errorf("%s status = %d, want 409", name, got)
		}
	}
}

// mysqlView runs on a SQLite database while telling the ORM it is MySQL (no
// RETURNING); SQLite also accepts the backtick quoting.
type mysqlView struct{ database.Executor }

func (mysqlView) Dialect() database.Dialect { return database.MySQL }

// Without RETURNING an upsert reads the version the row ended up with.
func TestReadVersionAfterUpsertWithoutReturning(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if _, err := db.ExecContext(
		ctx,
		`CREATE TABLE ver_items (id INTEGER PRIMARY KEY AUTOINCREMENT, code TEXT NOT NULL UNIQUE, note TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 0);
`+`INSERT INTO ver_items (code, note, version) VALUES ('a', 'x', 7)`,
	); err != nil {
		t.Fatal(err)
	}

	m := metaOf[verItem]()
	ex := mysqlView{db}

	item := verItem{Code: "a"}
	if err := readVersion(
		ctx,
		ex,
		m,
		[]string{"code"},
		reflect.ValueOf(&item).Elem(),
	); err != nil ||
		item.Version != 7 {
		t.Fatalf("readVersion = version %d, %v; want 7", item.Version, err)
	}

	missing := verItem{Code: "zzz"}

	err := readVersion(ctx, ex, m, []string{"code"}, reflect.ValueOf(&missing).Elem())
	if !errors.Is(err, sql.ErrNoRows) || !strings.Contains(err.Error(), "read the version") {
		t.Errorf("readVersion of a missing row = %v, want a wrapped sql.ErrNoRows", err)
	}
}

func TestColumnName(t *testing.T) {
	t.Parallel()

	if got := Col[User, string]("name").Name(); got != "name" {
		t.Errorf("Col(name).Name() = %q", got)
	}
}

func TestRawEachReportsQueryAndScanErrors(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	for _, err := range RawEach[User](ctx, db, "SELECT * FROM no_such_table") {
		if err == nil || !strings.Contains(err.Error(), "orm: raw") {
			t.Errorf("query error = %v, want an orm: raw error", err)
		}

		break
	}

	// A text column cannot be scanned into an int.
	var scanErr error

	for _, err := range RawEach[int](ctx, db, "SELECT name FROM users") {
		scanErr = err
		break
	}

	if scanErr == nil {
		t.Error("scanning a name into an int = nil error")
	}

	var n int

	for _, err := range RawEach[int](ctx, db, "SELECT age FROM users WHERE age > ?", 0) {
		if err != nil {
			t.Fatal(err)
		}

		n++

		break // stopping the loop closes the rows
	}

	if n != 1 {
		t.Errorf("rows read before breaking = %d, want 1", n)
	}
}

// Every time the ORM binds is written in UTC, whatever form it came in.
func TestUTCConvertsEveryTimeForm(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("UTC+5", 5*3600)
	local := time.Date(2026, 1, 2, 15, 0, 0, 0, zone)
	want := local.UTC()

	var nilTime *time.Time

	tests := map[string]struct {
		in   any
		want any
	}{
		"time":        {local, want},
		"pointer":     {&local, want},
		"nil pointer": {nilTime, nilTime},
		"null time":   {sql.NullTime{Time: local, Valid: true}, sql.NullTime{Time: want, Valid: true}},
		"generic":     {sql.Null[time.Time]{V: local, Valid: true}, sql.Null[time.Time]{V: want, Valid: true}},
		"orm null": {
			NullTime{sql.Null[time.Time]{V: local, Valid: true}},
			NullTime{sql.Null[time.Time]{V: want, Valid: true}},
		},
		"other": {"text", "text"},
	}

	for name, tt := range tests {
		got := utc(tt.in)

		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: utc(%v) = %v, want %v", name, tt.in, got, tt.want)
		}
	}
}
