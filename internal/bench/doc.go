// Package bench holds comparative benchmarks: Vukem against net/http, chi
// and echo (routing, middleware, full request), encoding/json and
// html/template (rendering) and raw database/sql (ORM).
//
// It is its own module so chi and echo never reach the core's go.mod. Run
// it with `task bench`; results live in docs/performance.md.
package bench
