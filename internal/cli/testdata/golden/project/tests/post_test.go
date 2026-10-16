package tests

import (
	"testing"

	"github.com/AlexGalhardo/vukem/vukemtest"

	"example.com/blog/bootstrap"
)

func TestPost(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)

	app.Client(t).Get("/up").AssertOK()
}
