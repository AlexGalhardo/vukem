package vukem_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func Example() {
	// Apps read APP_KEY and the rest from the environment and .env files;
	// a map source keeps the example self-contained.
	src := config.MapSource(map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "test"})

	app, err := vukem.New(
		vukem.WithSource(src),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithRoutes(func(r *router.Router, _ *vukem.App) error {
			r.Get("/hello/{name}", func(c *web.Ctx) error {
				return c.String(http.StatusOK, "Hello, "+c.Param("name")+"!")
			})
			return nil
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	h, err := app.Handler()
	if err != nil {
		fmt.Println(err)
		return
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello/gopher", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	fmt.Println(rec.Code, string(body))
	// Output:
	// 200 Hello, gopher!
}

func ExampleMain() {
	// The main function of a generated project: Main parses the command
	// line (serve, migrate, queue:work...) and returns the exit code.
	os.Exit(vukem.Main(func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(opts, vukem.WithProviders(vukem.ProviderFunc(func(app *vukem.App) error {
			app.Log.Info("registering services")
			return nil
		})))...)
	}))
}
