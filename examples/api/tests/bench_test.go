package tests

import (
	"testing"

	"github.com/AlexGalhardo/vukem/examples/api/app/benchmarks"
	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestRateLimitScale(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New, vukemtest.Options{Env: map[string]string{"RATE_LIMIT_SCALE": "3"}})
	c := app.Client(t).WithToken(apiUser(t, app, "scaled@example.com"))

	c.GetJSON("/api/v1/tasks").AssertOK().AssertHeader("RateLimit-Limit", "180")
}

func TestRateLimitScaleRejectsBadValues(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"0", "-1", "abc", "10001"} {
		if _, err := vukemtest.Build(
			t,
			bootstrap.New,
			vukemtest.Options{Env: map[string]string{"RATE_LIMIT_SCALE": v}},
		); err == nil {
			t.Errorf("RATE_LIMIT_SCALE=%q: want an error", v)
		}
	}
}

// The bench user must be able to log in, or every iteration of task_flow fails.
func TestTaskFlowSetupCreatesLoginUser(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)

	if err := benchmarks.TaskFlow().Setup(t.Context(), app.App); err != nil {
		t.Fatal(err)
	}

	app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": "bench@example.com", "password": "correct horse battery", "device_name": "bench",
		"abilities": []string{"read", "write"},
	}).AssertCreated()
}
