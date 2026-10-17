// Package vukem is a full-stack web framework for Go inspired by Laravel and
// AdonisJS, built on net/http and the standard library.
//
// An [App] is built by [New] from functional options ([WithRoutes],
// [WithViews], [WithMigrations], [WithJobs], [WithProviders]...) and holds
// the configuration, logger, router and the lazily opened services (DB,
// Cache, Queue, Mailer, Sessions). Providers register services in two
// phases (Register, then Boot); runners such as the HTTP server run until
// the context is cancelled and shut down in reverse order. [Main] is the
// entry point of a generated project: it parses the command line (serve,
// migrate, queue:work, schedule:work...) and runs the matching command.
//
//	func main() { os.Exit(vukem.Main(bootstrap.New)) }
//
// There are no globals, no init() registration and no reflection-based
// dependency injection: everything an app uses is wired in code.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/architecture.md.
package vukem
