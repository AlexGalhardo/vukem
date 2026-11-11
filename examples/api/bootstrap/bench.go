package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/examples/api/app/benchmarks"
)

// Bench lists the load-test scenarios; vukem make:bench appends here.
func Bench() []vukem.BenchScenario {
	return []vukem.BenchScenario{
		benchmarks.TaskFlow(),
	}
}
