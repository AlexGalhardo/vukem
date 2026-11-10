package orm

import (
	"testing"

	"github.com/AlexGalhardo/vukem/uuid"
)

type uuTag struct {
	ID    uuid.UUID `db:"id"`
	Label string
}

func (uuTag) Table() string { return "uu_tags" }

type uuPost struct {
	ID   uuid.UUID `db:"id"`
	Name string

	Tags     ManyToMany[uuTag]  `orm:"pivot:uu_post_tag,fk:post_id,rk:tag_id"`
	Comments HasMany[uuComment] `orm:"fk:post_id"`
}

func (uuPost) Table() string { return "uu_posts" }

type uuComment struct {
	ID     uuid.UUID `db:"id"`
	PostID uuid.UUID `db:"post_id"`
	Body   string

	Post BelongsTo[uuPost] `orm:"fk:post_id"`
}

func (uuComment) Table() string { return "uu_comments" }

func TestManyToManyUUIDKeys(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	for _, stmt := range []string{
		`CREATE TABLE uu_tags (id TEXT PRIMARY KEY, label TEXT NOT NULL)`,
		`CREATE TABLE uu_posts (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE uu_comments (id TEXT PRIMARY KEY, post_id TEXT NOT NULL, body TEXT NOT NULL)`,
		`CREATE TABLE uu_post_tag (post_id TEXT NOT NULL, tag_id TEXT NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	post := uuPost{ID: uuid.NewV4(), Name: "p"}
	tagA := uuTag{ID: uuid.NewV4(), Label: "a"}
	tagB := uuTag{ID: uuid.NewV4(), Label: "b"}
	comment := uuComment{ID: uuid.NewV4(), PostID: post.ID, Body: "hi"}

	if err := Create(ctx, db, &post); err != nil {
		t.Fatal(err)
	}

	for _, tag := range []*uuTag{&tagA, &tagB} {
		if err := Create(ctx, db, tag); err != nil {
			t.Fatal(err)
		}
	}

	if err := Create(ctx, db, &comment); err != nil {
		t.Fatal(err)
	}

	pivotRows := func() int {
		t.Helper()

		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uu_post_tag`).Scan(&n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	if err := Attach(ctx, db, &post, "Tags", tagA.ID, tagB.ID); err != nil {
		t.Fatal(err)
	}

	loaded, err := QueryOf[uuPost](db).With("Tags").First(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if tags, err := loaded.Tags.Get(); err != nil || len(tags) != 2 {
		t.Fatalf("With(Tags) = %v, %v; want 2 tags", tags, err)
	}

	if err := Attach(ctx, db, &post, "Tags", tagA.ID, tagB.ID); err != nil {
		t.Fatal(err)
	}

	if n := pivotRows(); n != 2 {
		t.Fatalf("pivot rows after repeated Attach = %d, want 2", n)
	}

	if err := Sync(ctx, db, &post, "Tags", tagA.ID, tagB.ID); err != nil {
		t.Fatal(err)
	}

	if n := pivotRows(); n != 2 {
		t.Fatalf("pivot rows after same-set Sync = %d, want 2", n)
	}

	if err := Sync(ctx, db, &post, "Tags", tagA.ID); err != nil {
		t.Fatal(err)
	}

	if n := pivotRows(); n != 1 {
		t.Fatalf("pivot rows after narrowing Sync = %d, want 1", n)
	}

	withComments, err := QueryOf[uuPost](db).With("Comments").First(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if cs, err := withComments.Comments.Get(); err != nil || len(cs) != 1 || cs[0].ID != comment.ID {
		t.Fatalf("With(Comments) = %v, %v", cs, err)
	}

	withPost, err := QueryOf[uuComment](db).With("Post").First(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if p, err := withPost.Post.Get(); err != nil || p == nil || p.ID != post.ID {
		t.Fatalf("With(Post) = %v, %v", p, err)
	}
}
