package cachetest_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/cache/cachetest"
)

// A store implementation runs the suite from one test function.
func ExampleRun() {
	_ = func(t *testing.T) {
		t.Helper()

		cachetest.Run(t, cache.NewMemory(1000))
	}
}
