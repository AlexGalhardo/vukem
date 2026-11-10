package admin_test

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// TestSecurity_A01_AdminActionAuthorizedOnLockedRecord changes the row
// between the handler's early policy check and the action's transaction,
// the window another admin can write in.
func TestSecurity_A01_AdminActionAuthorizedOnLockedRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		change     string // SQL run once, at the first Approve check
		wantStatus int
		wantRuns   int32
		wantRow    map[string]any
	}{
		{
			name:       "policy decided on the locked row",
			change:     "UPDATE posts SET title = 'Locked' WHERE id = 1",
			wantStatus: 403,
			wantRuns:   0,
			wantRow:    map[string]any{"id": 1, "status": "draft", "title": "Locked"},
		},
		{
			name:       "concurrent column change kept",
			change:     "UPDATE posts SET body = 'edited by someone else' WHERE id = 1",
			wantStatus: 303,
			wantRuns:   1,
			wantRow:    map[string]any{"id": 1, "status": "live", "body": "edited by someone else"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				runs atomic.Int32
				once sync.Once
			)

			app := newApp(t, options{
				resource: func() []admin.AnyResource {
					return []admin.AnyResource{
						admin.NewResource[Post](postSchema).Actions(admin.Action[Post]{
							Name: "approve",
							Run: func(_ context.Context, _ *database.Tx, p *Post, _ string) error {
								runs.Add(1)
								p.Status = "live"
								return nil
							},
						}),
					}
				},
				racingApprove: func(ctx context.Context, db *database.DB) {
					once.Do(func() {
						if _, err := db.ExecContext(ctx, tt.change); err != nil {
							t.Errorf("racing change: %v", err)
						}
					})
				},
			})
			createPost(t, app, Post{Title: "First", Body: "original", Status: "draft"})

			as(t, app, "admin").PostForm("/admin/posts/1/actions/approve", url.Values{}).AssertStatus(tt.wantStatus)

			if got := runs.Load(); got != tt.wantRuns {
				t.Errorf("Run calls = %d, want %d", got, tt.wantRuns)
			}

			vukemtest.AssertDatabaseHas(t, app, "posts", tt.wantRow)
		})
	}
}
