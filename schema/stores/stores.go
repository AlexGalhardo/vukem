// Package stores ships the migrations for the tables vukem's own
// database-backed stores use: the queue's jobs and failed jobs tables,
// the sessions table, the cache table and the webhook delivery log.
//
// Pass them with the app's own migrations to let vukem migrate own that
// schema, so migrate:status lists it, migrate:fresh round-trips it and
// migrate:rollback reverts it:
//
//	vukem.New(
//		vukem.WithMigrations(slices.Concat(
//			stores.Migrations(stores.Options{}),
//			migrations.All(),
//		)),
//	)
//
// An app that does not register them keeps the previous behaviour: each
// store runs its own CREATE TABLE IF NOT EXISTS at boot. The statements
// are the same either way, so opting in (or out) never conflicts with
// tables that already exist. An app that uses only some of the stores can
// pass the per-package lists instead ([queue.Migrations],
// [session.Migrations], [cache.Migrations], [webhook.Migrations]).
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md#tables-the-framework-owns.
package stores

import (
	"cmp"
	"slices"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/webhook"
)

// Options names the framework's tables for apps that moved them, for
// example under a vukem_ prefix. An empty field keeps that store's
// default name, so the zero Options describes a default app. The names
// must match what the stores read at run time (QUEUE_TABLE,
// QUEUE_FAILED_TABLE, CACHE_TABLE, session.WithTable and the table passed
// to webhook.NewSQLDeliveryLog), otherwise the migration manages a table
// nothing uses.
type Options struct {
	// Jobs holds pending jobs (QUEUE_TABLE, default "jobs").
	Jobs string
	// FailedJobs holds failed jobs (QUEUE_FAILED_TABLE, default
	// "failed_jobs").
	FailedJobs string
	// Sessions holds server-side sessions (default "sessions").
	Sessions string
	// Cache holds cache entries (CACHE_TABLE, default "cache").
	Cache string
	// WebhookDeliveries holds webhook delivery attempts (default
	// "webhook_deliveries").
	WebhookDeliveries string
}

// Tables returns the tables vukem owns under o, in migration order: the
// queue's two tables, the sessions table, the cache table and the webhook
// delivery log. An app can print it to see which names are taken before
// naming a table of its own.
func Tables(o Options) []string {
	jobs, failed := o.queue()

	return []string{jobs, failed, o.sessions(), o.cache(), o.deliveries()}
}

// Migrations returns the migration of every framework store, in
// dependency-free order. The ids are fixed, so a database records them
// once however the app composes the list.
func Migrations(o Options) []schema.Migration {
	jobs, failed := o.queue()

	return slices.Concat(
		queue.Migrations(queue.DatabaseOptions{Table: jobs, FailedTable: failed}),
		session.Migrations(o.sessions()),
		cache.Migrations(o.cache()),
		webhook.Migrations(o.deliveries()),
	)
}

func (o Options) queue() (jobs, failed string) {
	return cmp.Or(o.Jobs, queue.DefaultTable), cmp.Or(o.FailedJobs, queue.DefaultFailedTable)
}

func (o Options) sessions() string { return cmp.Or(o.Sessions, session.DefaultTable) }

func (o Options) cache() string { return cmp.Or(o.Cache, cache.DefaultTable) }

func (o Options) deliveries() string {
	return cmp.Or(o.WebhookDeliveries, webhook.DefaultDeliveryTable)
}
