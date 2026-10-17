// Package mongo connects Vukem to MongoDB with a typed collection API. It
// is deliberately not the SQL query builder: filters and updates are BSON.
//
//	m, err := mongo.Open(ctx, os.Getenv("MONGO_URL"), os.Getenv("MONGO_DATABASE"))
//	users := mongo.Collection[User](m, "users")
//	u, err := users.FindOne(ctx, bson.D{{Key: "email", Value: email}})
//	for u, err := range users.Find(ctx, bson.D{{Key: "active", Value: true}}) { ... }
//
// Never pass decoded request JSON as a filter: operators such as $where or
// $ne would come from the client. Build filters from validated fields.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
)

// ErrNotFound is returned by FindOne when no document matches.
var ErrNotFound = errors.New("mongo: document not found")

// Client is a connection pool bound to a database.
type Client struct {
	client *mongo.Client
	db     *mongo.Database
}

// Open connects to uri and pings the primary, so a server that is down
// fails boot. The database is db when given and non-empty, else the URI
// path (mongodb://host/app):
//
//	mongo.Open(ctx, os.Getenv("MONGO_URL"), os.Getenv("MONGO_DATABASE"))
//
// A database in the URI path is also the default authSource; when the
// user lives in admin (managed providers), keep the path empty and pass
// db, or add ?authSource=admin. Use OpenLazy to start without the server.
func Open(ctx context.Context, uri string, db ...string) (*Client, error) {
	m, err := OpenLazy(uri, db...)
	if err != nil {
		return nil, err
	}

	if err := m.Ping(ctx); err != nil {
		_ = m.client.Disconnect(context.WithoutCancel(ctx))
		return nil, err
	}

	return m, nil
}

// OpenLazy is Open without the ping: it validates uri and the database
// name but never contacts the server, so the app boots while MongoDB is
// down and connects on first use. Call Ping from a readiness check.
func OpenLazy(uri string, db ...string) (*Client, error) {
	cs, err := connstring.ParseAndValidate(uri)
	if err != nil {
		return nil, fmt.Errorf("mongo: parse uri: %w", err)
	}

	name := cs.Database
	if len(db) > 0 && db[0] != "" {
		name = db[0]
	}

	if name == "" {
		return nil, errors.New("mongo: no database: pass it to Open (e.g. MONGO_DATABASE) or set the URI path")
	}

	opts := options.Client().ApplyURI(uri)
	if !cs.ServerSelectionTimeoutSet {
		opts.SetServerSelectionTimeout(10 * time.Second)
	}

	c, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	return &Client{client: c, db: c.Database(name)}, nil
}

// Ping checks the primary is reachable; use it in a readiness check.
func (m *Client) Ping(ctx context.Context) error {
	if err := m.client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}

	return nil
}

// Close disconnects.
func (m *Client) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

// Database returns the bound database.
func (m *Client) Database() *mongo.Database { return m.db }

// Raw returns the underlying driver client.
func (m *Client) Raw() *mongo.Client { return m.client }

// Coll is a typed collection: documents decode into T.
type Coll[T any] struct {
	c *mongo.Collection
}

// Collection returns the typed collection name.
func Collection[T any](m *Client, name string) *Coll[T] { return &Coll[T]{c: m.db.Collection(name)} }

// Raw returns the underlying collection (indexes, change streams...).
func (c *Coll[T]) Raw() *mongo.Collection { return c.c }

// FindOne returns the first matching document or ErrNotFound.
func (c *Coll[T]) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) (T, error) {
	var doc T

	err := c.c.FindOne(ctx, filter, opts...).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return doc, ErrNotFound
	}

	return doc, err
}

// Find iterates over the matching documents; stop early by breaking out of
// the loop. A non-nil error ends the iteration.
func (c *Coll[T]) Find(
	ctx context.Context,
	filter any,
	opts ...options.Lister[options.FindOptions],
) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T

		cur, err := c.c.Find(ctx, filter, opts...)
		if err != nil {
			yield(zero, err)
			return
		}
		defer func() { _ = cur.Close(context.WithoutCancel(ctx)) }()

		for cur.Next(ctx) {
			var doc T
			if err := cur.Decode(&doc); err != nil {
				yield(zero, err)
				return
			}

			if !yield(doc, nil) {
				return
			}
		}

		if err := cur.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// All returns every matching document.
func (c *Coll[T]) All(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	var out []T

	for doc, err := range c.Find(ctx, filter, opts...) {
		if err != nil {
			return nil, err
		}

		out = append(out, doc)
	}

	return out, nil
}

// Count returns the number of matching documents.
func (c *Coll[T]) Count(ctx context.Context, filter any) (int64, error) {
	return c.c.CountDocuments(ctx, filter)
}

// Insert stores doc and returns its _id (generated when doc has none).
func (c *Coll[T]) Insert(ctx context.Context, doc T) (any, error) {
	res, err := c.c.InsertOne(ctx, doc)
	if err != nil {
		return nil, err
	}

	return res.InsertedID, nil
}

// InsertMany stores docs and returns their _ids in order.
func (c *Coll[T]) InsertMany(ctx context.Context, docs []T) ([]any, error) {
	res, err := c.c.InsertMany(ctx, docs)
	if err != nil {
		return nil, err
	}

	return res.InsertedIDs, nil
}

// UpdateOne applies update (e.g. bson.D{{"$set", ...}}) to the first match
// and reports how many documents matched and changed.
func (c *Coll[T]) UpdateOne(ctx context.Context, filter, update any) (matched, modified int64, err error) {
	res, err := c.c.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, 0, err
	}

	return res.MatchedCount, res.ModifiedCount, nil
}

// UpdateMany applies update to every match.
func (c *Coll[T]) UpdateMany(ctx context.Context, filter, update any) (matched, modified int64, err error) {
	res, err := c.c.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, 0, err
	}

	return res.MatchedCount, res.ModifiedCount, nil
}

// ReplaceOne replaces the first match with doc.
func (c *Coll[T]) ReplaceOne(ctx context.Context, filter any, doc T) (matched int64, err error) {
	res, err := c.c.ReplaceOne(ctx, filter, doc)
	if err != nil {
		return 0, err
	}

	return res.MatchedCount, nil
}

// DeleteOne deletes the first match.
func (c *Coll[T]) DeleteOne(ctx context.Context, filter any) (int64, error) {
	res, err := c.c.DeleteOne(ctx, filter)
	if err != nil {
		return 0, err
	}

	return res.DeletedCount, nil
}

// DeleteMany deletes every match.
func (c *Coll[T]) DeleteMany(ctx context.Context, filter any) (int64, error) {
	res, err := c.c.DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}

	return res.DeletedCount, nil
}

// Aggregate runs pipeline on c and decodes the results into R.
func Aggregate[R, T any](ctx context.Context, c *Coll[T], pipeline any) ([]R, error) {
	cur, err := c.c.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}

	var out []R
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}

	return out, nil
}
