package graphql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/contrib/graphql"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
)

type User struct {
	orm.Model
	Name string `json:"name"`
}

type Post struct {
	orm.Model
	Title  string `json:"title"`
	UserID int64  `json:"-"`
}

// countingDB counts SELECTs so tests can assert the N+1 problem is gone.
type countingDB struct {
	*database.DB
	queries atomic.Int32
}

func (c *countingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.queries.Add(1)
	return c.DB.QueryContext(ctx, q, args...)
}

func blogDB(t *testing.T, users, postsPerUser int) *countingDB {
	t.Helper()

	ctx := t.Context()

	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, ddl := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP, name TEXT)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP, title TEXT, user_id INTEGER)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	for u := range users {
		user := User{Name: fmt.Sprintf("user %d", u+1)}
		if err := orm.Create(ctx, db, &user); err != nil {
			t.Fatal(err)
		}

		for p := range postsPerUser {
			if err := orm.Create(
				ctx,
				db,
				&Post{Title: fmt.Sprintf("post %d.%d", u+1, p+1), UserID: user.ID},
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	return &countingDB{DB: db}
}

const blogSDL = `
type Query { posts: [Post!]! }
type Post { id: ID! title: String! author: User! }
type User { id: ID! name: String! posts: [Post!]! }
`

func blogSchema(t *testing.T, db *countingDB) *graphql.Schema {
	t.Helper()

	// BatchWait(0): only the resolution tick dispatches batches. The
	// fallback timer fires after 10ms without progress, which a stalled
	// test process can still produce, making the query count depend on
	// wall time.
	users := graphql.NewLoader(graphql.ByKey(orm.QueryOf[User](db), "id", func(u *User) int64 { return u.ID }),
		graphql.BatchWait(0))
	postsByUser := graphql.NewLoader(graphql.GroupByKey(orm.QueryOf[Post](db).OrderBy("id", orm.Asc), "user_id",
		func(p *Post) int64 { return p.UserID }), graphql.BatchWait(0))

	s, err := graphql.New(blogSDL,
		graphql.Query("posts", func(ctx context.Context, _ graphql.NoArgs) ([]Post, error) {
			return orm.QueryOf[Post](db).OrderBy("id", orm.Asc).Get(ctx)
		}),
		graphql.Field("Post", "author", func(ctx context.Context, p *Post, _ graphql.NoArgs) (*User, error) {
			return users.Load(ctx, p.UserID)
		}),
		graphql.Field("User", "posts", func(ctx context.Context, u *User, _ graphql.NoArgs) ([]*Post, error) {
			return postsByUser.Load(ctx, u.ID)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestLoaderAvoidsNPlusOne(t *testing.T) {
	t.Parallel()

	db := blogDB(t, 5, 4) // 20 posts by 5 users
	s := blogSchema(t, db)

	db.queries.Store(0)

	resp := s.Execute(t.Context(), graphql.Request{Query: `{ posts { title author { name posts { title } } } }`})
	if len(resp.Errors) > 0 {
		t.Fatalf("Execute errors = %v", resp.Errors)
	}

	// 1 for posts, 1 batch for the 5 authors, 1 batch for their posts:
	// without batching this would be 1 + 20 + 20 = 41.
	if got := db.queries.Load(); got != 3 {
		t.Errorf("SQL queries = %d, want 3", got)
	}

	var data struct {
		Posts []struct {
			Title  string `json:"title"`
			Author struct {
				Name  string `json:"name"`
				Posts []struct {
					Title string `json:"title"`
				} `json:"posts"`
			} `json:"author"`
		} `json:"posts"`
	}

	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatal(err)
	}

	if len(data.Posts) != 20 || data.Posts[5].Author.Name != "user 2" || len(data.Posts[5].Author.Posts) != 4 ||
		data.Posts[5].Author.Posts[3].Title != "post 2.4" {
		t.Errorf("data = %s", resp.Data)
	}
}

func TestLoaderCachesPerRequest(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		batches [][]int
	)

	l := graphql.NewLoader(func(_ context.Context, keys []int) (map[int]string, error) {
		mu.Lock()
		batches = append(batches, slices.Sorted(slices.Values(keys)))
		mu.Unlock()

		out := map[int]string{}
		for _, k := range keys {
			if k != 404 {
				out[k] = fmt.Sprint("v", k)
			}
		}

		return out, nil
	}, graphql.MaxBatch(2))

	type keyArgs struct {
		K int `json:"k"`
	}

	s, err := graphql.New(`type Query { v(k: Int!): String many(ks: [Int!]!): [String] }`,
		graphql.Query("v", func(ctx context.Context, a keyArgs) (*string, error) {
			v, err := l.Load(ctx, a.K)
			if v == "" {
				return nil, err
			}

			return &v, err
		}),
		graphql.Query("many", func(ctx context.Context, a struct {
			Ks []int `json:"ks"`
		},
		) ([]string, error) {
			return l.LoadMany(ctx, a.Ks)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	resp := s.Execute(
		t.Context(),
		graphql.Request{Query: `{ a: v(k: 1) b: v(k: 2) c: v(k: 1) d: v(k: 404) e: many(ks: [2, 3]) }`},
	)
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	if want := `{"data":{"a":"v1","b":"v2","c":"v1","d":null,"e":["v2","v3"]}}`; string(got) != want {
		t.Errorf("Execute = %s, want %s", got, want)
	}

	// Keys 1, 2, 404, 3 once each (cached within the request), in batches of at most 2.
	var all []int
	for _, b := range batches {
		if len(b) > 2 {
			t.Errorf("batch %v exceeds MaxBatch(2)", b)
		}

		all = append(all, b...)
	}

	slices.Sort(all)

	if diff := cmp.Diff([]int{1, 2, 3, 404}, all); diff != "" {
		t.Errorf("loaded keys (-want +got):\n%s", diff)
	}

	// A second request has its own cache.
	s.Execute(t.Context(), graphql.Request{Query: `{ v(k: 1) }`})

	if last := batches[len(batches)-1]; !slices.Equal(last, []int{1}) {
		t.Errorf("second request batch = %v, want [1]", last)
	}
}

func TestLoaderErrorsAndOutsideExecute(t *testing.T) {
	t.Parallel()

	calls := 0
	l := graphql.NewLoader(func(_ context.Context, keys []string) (map[string]int, error) {
		calls++
		if slices.Contains(keys, "bad") {
			return nil, errors.New("db down")
		}

		return map[string]int{keys[0]: len(keys[0])}, nil
	})

	// Without an Execute scope the batch function is called directly.
	if v, err := l.Load(t.Context(), "abc"); v != 3 || err != nil || calls != 1 {
		t.Errorf("Load outside Execute = %d, %v (calls %d), want 3, nil (1)", v, err, calls)
	}

	if _, err := l.Load(t.Context(), "bad"); err == nil {
		t.Error("Load(bad) error = nil, want db down")
	}

	panicky := graphql.NewLoader(func(context.Context, []int) (map[int]int, error) { panic("boom") })
	if _, err := panicky.Load(t.Context(), 1); err == nil {
		t.Error("panicking batch error = nil")
	}
}

func TestLoaderUntrackedGoroutines(t *testing.T) {
	t.Parallel()

	var batches atomic.Int32

	l := graphql.NewLoader(func(_ context.Context, keys []int) (map[int]int, error) {
		batches.Add(1)

		out := map[int]int{}
		for _, k := range keys {
			out[k] = k * 10
		}

		return out, nil
	})

	// A resolver fanning out with its own goroutines and a mutex must not
	// deadlock the loader.
	s, err := graphql.New(`type Query { sum: Int }`,
		graphql.Query("sum", func(ctx context.Context, _ graphql.NoArgs) (int, error) {
			var (
				wg  sync.WaitGroup
				mu  sync.Mutex
				sum int
			)

			for k := range 3 {
				wg.Go(func() {
					mu.Lock()
					defer mu.Unlock()

					v, _ := l.Load(ctx, k+1)
					sum += v
				})
			}

			wg.Wait()

			return sum, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	resp := s.Execute(t.Context(), graphql.Request{Query: `{ sum }`})
	if got := string(resp.Data); got != `{"sum":60}` {
		t.Errorf("Execute = %s %v, want {\"sum\":60}", got, resp.Errors)
	}
}

type Item struct {
	ID int `json:"id"`
}

// TestLoaderBatchWaitWaitsForProgress models a loaded machine: sibling
// resolvers reach Load one by one, 6ms apart, so the whole round takes
// longer than BatchWait. While siblings keep making progress the fallback
// timer must not split the batch; the resolution tick sends it once.
func TestLoaderBatchWaitWaitsForProgress(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var batches [][]int

		l := graphql.NewLoader(func(_ context.Context, keys []int) (map[int]string, error) {
			batches = append(batches, slices.Sorted(slices.Values(keys)))

			out := map[int]string{}
			for _, k := range keys {
				out[k] = fmt.Sprint("v", k)
			}

			return out, nil
		})

		s, err := graphql.New(`type Query { items: [Item!]! } type Item { id: Int! v: String! }`,
			graphql.Query("items", func(context.Context, graphql.NoArgs) ([]Item, error) {
				return []Item{{0}, {1}, {2}, {3}, {4}}, nil
			}),
			graphql.Field("Item", "v", func(ctx context.Context, it *Item, _ graphql.NoArgs) (string, error) {
				time.Sleep(time.Duration(it.ID) * 6 * time.Millisecond)
				return l.Load(ctx, it.ID)
			}),
		)
		if err != nil {
			t.Fatal(err)
		}

		resp := s.Execute(t.Context(), graphql.Request{Query: `{ items { v } }`})
		if len(resp.Errors) > 0 {
			t.Fatalf("Execute errors = %v", resp.Errors)
		}

		if diff := cmp.Diff([][]int{{0, 1, 2, 3, 4}}, batches); diff != "" {
			t.Errorf("batches mismatch (-want +got):\n%s", diff)
		}
	})
}

// TestLoaderBatchWaitFallback: a resolver blocked on something other than
// a loader holds back the resolution tick, so the pending batch is sent
// once the operation made no progress for BatchWait.
func TestLoaderBatchWaitFallback(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts []graphql.LoaderOption
		want time.Duration
	}{
		{"default", nil, graphql.DefaultBatchWait},
		{"custom", []graphql.LoaderOption{graphql.BatchWait(25 * time.Millisecond)}, 25 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				gate := make(chan struct{})

				var sentAfter time.Duration

				l := graphql.NewLoader(func(_ context.Context, keys []int) (map[int]int, error) {
					sentAfter = time.Since(start)
					close(gate)

					return map[int]int{keys[0]: 7}, nil
				}, tc.opts...)

				s, err := graphql.New(`type Query { blocked: Int loaded: Int }`,
					graphql.Query("blocked", func(context.Context, graphql.NoArgs) (int, error) {
						<-gate
						return 1, nil
					}),
					graphql.Query("loaded", func(ctx context.Context, _ graphql.NoArgs) (int, error) {
						return l.Load(ctx, 1)
					}),
				)
				if err != nil {
					t.Fatal(err)
				}

				resp := s.Execute(t.Context(), graphql.Request{Query: `{ blocked loaded }`})
				if got := string(resp.Data); got != `{"blocked":1,"loaded":7}` {
					t.Errorf("Execute = %s %v, want {\"blocked\":1,\"loaded\":7}", got, resp.Errors)
				}

				if sentAfter != tc.want {
					t.Errorf("batch sent after %v, want %v", sentAfter, tc.want)
				}
			})
		})
	}
}

// TestLoaderBatchWaitAgeCap: a chain of siblings that keeps making
// progress cannot hold the first keys until all of them finish; the batch
// is sent once its oldest key waited 10×BatchWait.
func TestLoaderBatchWaitAgeCap(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()

		var (
			batches [][]int
			sent    []time.Duration
		)

		l := graphql.NewLoader(func(_ context.Context, keys []int) (map[int]int, error) {
			batches = append(batches, slices.Sorted(slices.Values(keys)))
			sent = append(sent, time.Since(start))

			out := map[int]int{}
			for _, k := range keys {
				out[k] = k
			}

			return out, nil
		})

		items := make([]Item, 30)
		for i := range items {
			items[i].ID = i
		}

		s, err := graphql.New(`type Query { items: [Item!]! } type Item { id: Int! v: Int! }`,
			graphql.Query("items", func(context.Context, graphql.NoArgs) ([]Item, error) { return items, nil }),
			graphql.Field("Item", "v", func(ctx context.Context, it *Item, _ graphql.NoArgs) (int, error) {
				time.Sleep(time.Duration(it.ID) * 6 * time.Millisecond)
				return l.Load(ctx, it.ID)
			}),
		)
		if err != nil {
			t.Fatal(err)
		}

		resp := s.Execute(t.Context(), graphql.Request{Query: `{ items { v } }`})
		if len(resp.Errors) > 0 {
			t.Fatalf("Execute errors = %v", resp.Errors)
		}

		var first, second []int
		for i := range 30 {
			if i <= 16 {
				first = append(first, i)
			} else {
				second = append(second, i)
			}
		}

		if diff := cmp.Diff([][]int{first, second}, batches); diff != "" {
			t.Errorf("batches mismatch (-want +got):\n%s", diff)
		}

		if want := 10 * graphql.DefaultBatchWait; len(sent) == 0 || sent[0] != want {
			t.Errorf("first batch sent after %v, want %v", sent, want)
		}
	})
}
