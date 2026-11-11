package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"

	"example.com/blog/app/benchmarks"
)

// Bench lists the load-test scenarios; vukem make:bench appends here.
func Bench() []vukem.BenchScenario {
	return []vukem.BenchScenario{
		benchmarks.BrowsePosts(),
	}
}
