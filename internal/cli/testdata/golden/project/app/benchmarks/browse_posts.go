package benchmarks

import (
	"context"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/loadtest"
)

// BrowsePosts is a load-test scenario: one iteration is one user journey.
// Run it with "vukem bench --scenario browse_posts".
func BrowsePosts() vukem.BenchScenario {
	var s vukem.BenchScenario

	s.Name = "browse_posts"
	s.Weight = 1
	s.Do = func(ctx context.Context, c *loadtest.Client) error {
		_, err := c.Get(ctx, "/")
		return err
	}
	// Setup creates the data the scenario needs, once, before the load.
	s.Setup = func(_ context.Context, _ *vukem.App) error { return nil }

	return s
}
