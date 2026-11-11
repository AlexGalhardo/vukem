package studio

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// fakeMongo records what the source asks of MongoDB.
type fakeMongo struct {
	mu       sync.Mutex
	colls    []string
	docs     []bson.Raw
	findArgs struct {
		filter, sort bson.D
		skip, limit  int64
	}
	calls    []string
	matched  int64
	deleted  int64
	inserted bson.D
	replaced bson.D
}

func (f *fakeMongo) listCollections(context.Context) ([]string, error) { return f.colls, nil }

// addDoc appends a document while a handler may be reading.
func (f *fakeMongo) addDoc(d bson.Raw) {
	f.mu.Lock()
	f.docs = append(f.docs, d)
	f.mu.Unlock()
}

func (f *fakeMongo) estimate(context.Context, string) (int64, error) { return 7, nil }

func (f *fakeMongo) find(_ context.Context, _ string, filter, sort bson.D, skip, limit int64) ([]bson.Raw, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "find")
	f.findArgs.filter, f.findArgs.sort, f.findArgs.skip, f.findArgs.limit = filter, sort, skip, limit

	return f.docs, nil
}

func (f *fakeMongo) findID(context.Context, string, any) (bson.Raw, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "findID")

	if len(f.docs) == 0 {
		return nil, ErrNotFound
	}

	return f.docs[0], nil
}

func (f *fakeMongo) aggregate(context.Context, string, bson.A, int64) ([]bson.Raw, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "aggregate")
	return f.docs, nil
}

func (f *fakeMongo) insert(_ context.Context, _ string, doc bson.D) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "insert")
	f.inserted = doc

	return nil
}

func (f *fakeMongo) replace(_ context.Context, _ string, _ any, doc bson.D) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "replace")
	f.replaced = doc

	return f.matched, nil
}

func (f *fakeMongo) remove(context.Context, string, any) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "remove")
	return f.deleted, nil
}

func rawDoc(t *testing.T, id any, name string) bson.Raw {
	t.Helper()

	b, err := bson.Marshal(bson.D{{Key: "_id", Value: id}, {Key: "name", Value: name}})
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func newMongo(t *testing.T, write bool, docs ...bson.Raw) (*MongoDB, *fakeMongo) {
	t.Helper()

	f := &fakeMongo{colls: []string{"users", "posts"}, docs: docs, matched: 1, deleted: 1}

	return &MongoDB{
		name: "mongo", label: "mongodb  localhost:27017/app", api: f, write: write,
		ping: func(context.Context) error { return nil }, close: func() error { return nil },
	}, f
}

func TestMongoCollectionsAreSortedWithEstimates(t *testing.T) {
	t.Parallel()

	m, _ := newMongo(t, false)

	got, err := m.Collections(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []Collection{{"posts", 7}, {"users", 7}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("collections (-want +got):\n%s", diff)
	}
}

func TestMongoDocumentsKeysetPaging(t *testing.T) {
	t.Parallel()

	docs := []bson.Raw{rawDoc(t, int32(1), "a"), rawDoc(t, int32(2), "b"), rawDoc(t, int32(3), "c")}
	m, f := newMongo(t, false, docs...)

	page, err := m.Documents(t.Context(), DocQuery{
		Collection: "users", PerPage: 2, After: `{"$numberInt":"0"}`,
		Filters: []Filter{{"name", "ne", "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !page.HasMore || len(page.Docs) != 2 || page.Next != `{"$numberInt":"2"}` {
		t.Errorf("page = %+v, want 2 docs, more, next after _id 2", page)
	}

	if f.findArgs.limit != 3 || f.findArgs.skip != 0 {
		t.Errorf("limit = %d skip = %d, want one extra row to detect more, no skip", f.findArgs.limit, f.findArgs.skip)
	}

	// The cursor and the filter are combined on the server.
	if len(f.findArgs.filter) != 1 || f.findArgs.filter[0].Key != "$and" {
		t.Errorf("filter = %v, want the grid filter and the keyset condition in an $and", f.findArgs.filter)
	}

	if !strings.Contains(page.Docs[0].JSON, `"name": "a"`) || page.Docs[0].ID != `{"$numberInt":"1"}` {
		t.Errorf("doc = %+v", page.Docs[0])
	}
}

func TestMongoDocumentsOtherSortUsesSkip(t *testing.T) {
	t.Parallel()

	m, f := newMongo(t, false, rawDoc(t, int32(1), "a"))

	if _, err := m.Documents(
		t.Context(),
		DocQuery{Collection: "users", Sort: "name", Page: 3, PerPage: 10},
	); err != nil {
		t.Fatal(err)
	}

	if f.findArgs.skip != 20 {
		t.Errorf("skip = %d, want 20 for page 3 of 10", f.findArgs.skip)
	}

	if _, err := m.Documents(t.Context(), DocQuery{Collection: "users", Sort: "$where"}); err == nil {
		t.Error("an operator as sort field was accepted")
	}
}

func TestMongoUnknownCollectionFailsClosed(t *testing.T) {
	t.Parallel()

	m, f := newMongo(t, true)

	for _, call := range []func() error{
		func() error { _, err := m.Documents(t.Context(), DocQuery{Collection: "system.users"}); return err },
		func() error { _, err := m.Document(t.Context(), "nope", `"x"`); return err },
		func() error { _, err := m.Find(t.Context(), "nope", `{}`); return err },
		func() error { _, err := m.Aggregate(t.Context(), "nope", `[]`); return err },
		func() error { return m.Insert(t.Context(), "nope", `{}`) },
		func() error { return m.Replace(t.Context(), "nope", `"x"`, `{}`) },
		func() error { return m.Delete(t.Context(), "nope", `"x"`) },
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown collection = %v, want ErrNotFound", err)
		}
	}

	if len(f.calls) != 0 {
		t.Errorf("MongoDB was asked something for an unknown collection: %v", f.calls)
	}
}

func TestMongoConsoleRefusesBeforeTheServerSeesIt(t *testing.T) {
	t.Parallel()

	m, f := newMongo(t, false)

	if _, err := m.Find(t.Context(), "users", `{"$where": "sleep(1000)"}`); err == nil {
		t.Error("$where accepted by find")
	}

	if _, err := m.Aggregate(t.Context(), "users", `[{"$match": {}}, {"$out": "x"}]`); err == nil {
		t.Error("$out accepted without --write")
	}

	if _, err := m.Aggregate(t.Context(), "users", `[{"$group": {"_id": 1, "v": {"$accumulator": {}}}}]`); err == nil {
		t.Error("$accumulator accepted")
	}

	if len(f.calls) != 0 {
		t.Errorf("the server received %v", f.calls)
	}

	w, wf := newMongo(t, true)

	if _, err := w.Aggregate(
		t.Context(),
		"users",
		`[{"$match": {}}, {"$out": "x"}]`,
	); err != nil ||
		len(wf.calls) != 1 {
		t.Errorf("$out with --write = %v, calls %v", err, wf.calls)
	}
}

func TestMongoReplaceChecksIDAndMatch(t *testing.T) {
	t.Parallel()

	m, f := newMongo(t, true)

	// A different _id in the body: refused, nothing sent.
	if err := m.Replace(t.Context(), "users", `{"$numberInt":"1"}`, `{"_id": 2, "name": "x"}`); err == nil {
		t.Error("a body with another _id was accepted")
	}

	if len(f.calls) != 0 {
		t.Errorf("calls = %v", f.calls)
	}

	// No _id in the body: the route's is added.
	if err := m.Replace(t.Context(), "users", `{"$numberInt":"1"}`, `{"name": "x"}`); err != nil {
		t.Fatal(err)
	}

	if len(f.replaced) != 2 || f.replaced[0].Key != "_id" {
		t.Errorf("replaced = %v, want the _id first", f.replaced)
	}

	f.matched = 0
	if err := m.Replace(
		t.Context(),
		"users",
		`{"$numberInt":"1"}`,
		`{"name": "x"}`,
	); err == nil ||
		!strings.Contains(err.Error(), "matched 0") {
		t.Errorf("MatchedCount 0 = %v, want an error", err)
	}

	f.deleted = 0
	if err := m.Delete(t.Context(), "users", `{"$numberInt":"1"}`); err == nil {
		t.Error("DeletedCount 0 was not an error")
	}
}

func TestMongoWriteBodiesAreChecked(t *testing.T) {
	t.Parallel()

	m, f := newMongo(t, true)

	if err := m.Insert(t.Context(), "users", `{"a": {"$where": "1"}}`); err == nil {
		t.Error("$where in an inserted document was accepted")
	}

	if err := m.Insert(t.Context(), "users", `{"a": 1}`); err != nil || len(f.inserted) != 1 {
		t.Errorf("Insert = %v, inserted %v", err, f.inserted)
	}
}
