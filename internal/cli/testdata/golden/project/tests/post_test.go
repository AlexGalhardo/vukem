package tests

import (
	"testing"

	"github.com/AlexGalhardo/golang-framework/lumektest"

	"example.com/blog/bootstrap"
)

func TestPost(t *testing.T) {
	t.Parallel()

	app := lumektest.New(t, bootstrap.New)

	app.Client(t).Get("/up").AssertOK()
}
