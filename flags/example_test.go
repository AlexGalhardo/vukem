package flags_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/flags"
)

func Example() {
	newCheckout := flags.Bool("new_checkout", false).Describe("Serve the new checkout")
	adminMFA := flags.Bool("admin_two_factor", true).Lock()
	uploadLimit := flags.Int("upload_limit_mb", 10)

	// In an app: vukem.WithFlags(newCheckout, adminMFA, uploadLimit) and
	// app.Flags(), which adds the database store and the app cache.
	ff, err := flags.New(flags.Config{
		Flags: []flags.AnyFlag{newCheckout, adminMFA, uploadLimit},
		Env:   config.MapSource(map[string]string{"FLAG_UPLOAD_LIMIT_MB": "25"}),
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	ctx := context.Background()
	fmt.Println(ff.Enabled(ctx, newCheckout), ff.Enabled(ctx, adminMFA), flags.Get(ctx, ff, uploadLimit))
	fmt.Println(ff.Expose(ctx, newCheckout))
	// Output:
	// false true 25
	// map[new_checkout:false]
}
