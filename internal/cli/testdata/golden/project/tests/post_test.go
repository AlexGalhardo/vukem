package tests

import (
	"testing"

	"example.com/blog/bootstrap"

	"github.com/AlexGalhardo/golang-framework/lumektest"
)

func TestPost(t *testing.T) {
	t.Parallel()

	app := lumektest.New(t, bootstrap.New)

	app.Client(t).Get("/up").AssertOK()
}
