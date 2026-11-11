package benchmarks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/loadtest"
	"github.com/AlexGalhardo/vukem/orm"
)

const (
	benchEmail    = "bench@example.com"
	benchPassword = "correct horse battery"
)

// TaskFlow is one API session: exchange the password for a token, list the tasks, create one and list again.
// Run it with "vukem bench --scenario task_flow" (README, "Load test").
func TaskFlow() vukem.BenchScenario {
	var s vukem.BenchScenario

	s.Name = "task_flow"
	s.Weight = 1
	s.Do = func(ctx context.Context, c *loadtest.Client) error {
		login, err := c.PostJSON(ctx, "/api/v1/tokens", map[string]any{
			"email": benchEmail, "password": benchPassword, "device_name": "bench",
			"abilities": []string{"read", "write"},
		})
		if err != nil {
			return err
		}

		if login.Status != http.StatusCreated {
			return fmt.Errorf("login: status %d", login.Status)
		}

		var tok struct {
			Token string `json:"token"`
		}
		if err := login.JSON(&tok); err != nil {
			return err
		}

		c.SetToken(tok.Token)

		if _, err := c.Get(ctx, "/api/v1/tasks?per_page=10"); err != nil {
			return err
		}

		if _, err := c.PostJSON(ctx, "/api/v1/tasks", map[string]any{
			"title": fmt.Sprintf("Task %d", c.Seq()), "status": "todo",
		}); err != nil {
			return err
		}

		_, err = c.Get(ctx, "/api/v1/tasks?status=todo&sort=title&per_page=10")

		return err
	}
	// Setup creates the verified user the journey logs in as, once, before the load.
	s.Setup = func(ctx context.Context, app *vukem.App) error {
		acc, err := accounts.New(app)
		if err != nil {
			return err
		}

		h, err := acc.Hasher.Make(benchPassword)
		if err != nil {
			return err
		}

		u := models.User{Name: "Bench User", Email: benchEmail, Password: h}
		u.EmailVerifiedAt.V, u.EmailVerifiedAt.Valid = time.Now().UTC(), true

		return orm.Create(ctx, acc.DB, &u)
	}

	return s
}
