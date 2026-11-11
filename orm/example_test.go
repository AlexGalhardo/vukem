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

type Author struct {
	ID   int64 `db:"id"`
	Name string

	Books orm.HasMany[Book] `orm:"fk:author_id"`
}

type Book struct {
	ID       int64 `db:"id"`
	AuthorID int64
	Title    string
	Views    int64

	Author        orm.BelongsTo[Author] `orm:"fk:author_id"`
	Comments      orm.HasMany[Comment]  `orm:"fk:book_id"`
	CommentsCount int64                 `orm:"count:Comments"`
}

type Comment struct {
	ID       int64 `db:"id"`
	BookID   int64
	AuthorID int64
	Body     string

	Author orm.BelongsTo[Author] `orm:"fk:author_id"`
}

// queryExampleDB opens an in-memory database with two authors, three books
// and two comments.
func queryExampleDB() (*database.DB, context.Context) {
	ctx := context.Background()
	db, _ := database.Open(ctx, ":memory:")

	for _, stmt := range []string{
		`CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE books (id INTEGER PRIMARY KEY, author_id INTEGER, title TEXT, views INTEGER)`,
		`CREATE TABLE comments (id INTEGER PRIMARY KEY, book_id INTEGER, author_id INTEGER, body TEXT)`,
		`INSERT INTO authors VALUES (1, 'Ada'), (2, 'Bob')`,
		`INSERT INTO books VALUES (1, 1, 'Go', 100), (2, 1, 'SQL', 50), (3, 2, 'Zig', 10)`,
		`INSERT INTO comments VALUES (1, 1, 2, 'good'), (2, 1, 1, 'thanks')`,
	} {
		_, _ = db.ExecContext(ctx, stmt)
	}

	return db, ctx
}

func ExampleQuery_InnerJoin() {
	db, ctx := queryExampleDB()
	defer db.Close()

	type total struct {
		Name  string
		Views int64
	}

	rows, _ := orm.QueryOf[Book](db).
		InnerJoin[Author]("authors.id", "=", "books.author_id").
		SelectAs("authors.name", "name").
		SelectExpr(orm.Sum("books.views"), "views").
		GroupBy("authors.name").
		Having(orm.Sum("books.views"), ">", 20).
		Scan[total](ctx)

	fmt.Println(rows)
	// Output: [{Ada 150}]
}

func ExampleQuery_LeftJoin() {
	db, ctx := queryExampleDB()
	defer db.Close()

	n, _ := orm.QueryOf[Author](db).
		LeftJoin[Book]("books.author_id", "=", "authors.id").
		Distinct().Count(ctx)

	fmt.Println(n)
	// Output: 2
}

func ExampleQuery_JoinRelation() {
	db, ctx := queryExampleDB()
	defer db.Close()

	books, _ := orm.QueryOf[Book](db).JoinRelation("Author").
		Where("authors.name", "Bob").Get(ctx)

	fmt.Println(books[0].Title)
	// Output: Zig
}

func ExampleQuery_Sum() {
	db, ctx := queryExampleDB()
	defer db.Close()

	sum, _ := orm.QueryOf[Book](db).Sum[int64](ctx, "views")
	most, _ := orm.QueryOf[Book](db).Max[int64](ctx, "views")

	fmt.Println(sum, most)
	// Output: 160 100
}

func ExampleQuery_Scan() {
	db, ctx := queryExampleDB()
	defer db.Close()

	type perAuthor struct {
		AuthorID int64
		Books    int64
	}

	rows, _ := orm.QueryOf[Book](db).
		SelectAs("author_id", "author_id").
		SelectExpr(orm.Count("*"), "books").
		GroupBy("author_id").
		OrderBy("author_id", orm.Asc).
		Scan[perAuthor](ctx)

	fmt.Println(rows)
	// Output: [{1 2} {2 1}]
}

func ExampleQuery_WhereExists() {
	db, ctx := queryExampleDB()
	defer db.Close()

	commented := orm.QueryOf[Comment](db).WhereOuter[Book]("book_id", "=", "id")

	books, _ := orm.QueryOf[Book](db).WhereExists(commented).Get(ctx)

	fmt.Println(len(books), books[0].Title)
	// Output: 1 Go
}

func ExampleQuery_WhereInQuery() {
	db, ctx := queryExampleDB()
	defer db.Close()

	popular := orm.QueryOf[Book](db).Where("views", ">", 60).Select("author_id")

	authors, _ := orm.QueryOf[Author](db).WhereInQuery("id", popular).Get(ctx)

	fmt.Println(authors[0].Name)
	// Output: Ada
}

func ExampleQuery_WhereHas() {
	db, ctx := queryExampleDB()
	defer db.Close()

	books, _ := orm.QueryOf[Book](db).
		WhereHas[Comment]("Comments", func(q orm.Query[Comment]) orm.Query[Comment] {
		return q.Where("body", "good")
	}).Get(ctx)

	fmt.Println(books[0].Title)
	// Output: Go
}

func ExampleQuery_WithCount() {
	db, ctx := queryExampleDB()
	defer db.Close()

	books, _ := orm.QueryOf[Book](db).WithCount("Comments").OrderBy("id", orm.Asc).Get(ctx)

	fmt.Println(books[0].CommentsCount, books[1].CommentsCount)
	// Output: 2 0
}

func ExampleQuery_With_nested() {
	db, ctx := queryExampleDB()
	defer db.Close()

	books, _ := orm.QueryOf[Book](db).With("Comments.Author").OrderBy("id", orm.Asc).Get(ctx)

	comments, _ := books[0].Comments.Get()
	who, _ := comments[0].Author.Get()

	fmt.Println(who.Name)
	// Output: Bob
}

func ExampleQuery_WithWhere() {
	db, ctx := queryExampleDB()
	defer db.Close()

	books, _ := orm.QueryOf[Book](db).
		WithWhere[Comment]("Comments", func(q orm.Query[Comment]) orm.Query[Comment] {
		return q.Where("body", "thanks")
	}).OrderBy("id", orm.Asc).Get(ctx)

	comments, _ := books[0].Comments.Get()

	fmt.Println(len(comments), comments[0].Body)
	// Output: 1 thanks
}

func ExampleUpsert() {
	ctx := context.Background()
	db, _ := database.Open(ctx, ":memory:")

	defer db.Close()

	_, _ = db.ExecContext(
		ctx,
		`CREATE TABLE counters (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, hits INTEGER)`,
	)

	type Counter struct {
		ID   int64 `db:"id"`
		Name string
		Hits int64
	}

	on := orm.OnConflict{Columns: []string{"name"}, Update: []string{"hits"}}

	c := Counter{Name: "home", Hits: 1}
	_ = orm.Upsert(ctx, db, &c, on)

	again := Counter{Name: "home", Hits: 2}
	_ = orm.Upsert(ctx, db, &again, on)

	fmt.Println(c.ID == again.ID, again.Hits)
	// Output: true 2
}

func ExampleUpsertMany() {
	ctx := context.Background()
	db, _ := database.Open(ctx, ":memory:")

	defer db.Close()

	_, _ = db.ExecContext(
		ctx,
		`CREATE TABLE counters (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, hits INTEGER)`,
	)

	type Counter struct {
		ID   int64 `db:"id"`
		Name string
		Hits int64
	}

	on := orm.OnConflict{Columns: []string{"name"}, Update: []string{"hits"}}

	_ = orm.UpsertMany(ctx, db, []Counter{{Name: "a", Hits: 1}, {Name: "b", Hits: 1}}, on)
	_ = orm.UpsertMany(ctx, db, []Counter{{Name: "b", Hits: 5}, {Name: "c", Hits: 1}}, on)

	hits, _ := orm.Raw[int64](ctx, db, `SELECT hits FROM counters ORDER BY name`)

	fmt.Println(hits)
	// Output: [1 5 1]
}

func ExampleRaw() {
	db, ctx := queryExampleDB()
	defer db.Close()

	type row struct {
		Title string
		Views int64
	}

	rows, _ := orm.Raw[row](ctx, db, `SELECT title, views FROM books WHERE views > ? ORDER BY views DESC`, 20)

	fmt.Println(rows)
	// Output: [{Go 100} {SQL 50}]
}

func ExampleRawEach() {
	db, ctx := queryExampleDB()
	defer db.Close()

	for name, err := range orm.RawEach[string](ctx, db, `SELECT name FROM authors ORDER BY id`) {
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println(name)
	}
	// Output:
	// Ada
	// Bob
}
