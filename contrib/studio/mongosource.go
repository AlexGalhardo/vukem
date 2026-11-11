package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	vmongo "github.com/AlexGalhardo/vukem/drivers/mongo"
)

// APIs used, checked with go doc on mongo-driver v2.9.2: Database.
// ListCollectionNames, Collection.Find, FindOne, EstimatedDocumentCount,
// Aggregate, InsertOne, ReplaceOne, DeleteOne; bson.UnmarshalExtJSON and
// bson.MarshalExtJSON. Collection.Watch exists but --mongo-watch is not
// implemented yet (see live.go).

const (
	mongoTimeout  = 30 * time.Second
	maxConsoleDoc = 1000
	maxDocDisplay = 256 << 10
	maxSkipPages  = 1000
)

// ErrNotFound is returned for a collection, document or key that does not exist.
var ErrNotFound = errors.New("studio: not found")

// mongoAPI is the part of MongoDB the browser uses; tests replace it.
type mongoAPI interface {
	listCollections(ctx context.Context) ([]string, error)
	estimate(ctx context.Context, coll string) (int64, error)
	find(ctx context.Context, coll string, filter, sort bson.D, skip, limit int64) ([]bson.Raw, error)
	findID(ctx context.Context, coll string, id any) (bson.Raw, error)
	aggregate(ctx context.Context, coll string, pipeline bson.A, limit int64) ([]bson.Raw, error)
	insert(ctx context.Context, coll string, doc bson.D) error
	replace(ctx context.Context, coll string, id any, doc bson.D) (matched int64, err error)
	remove(ctx context.Context, coll string, id any) (deleted int64, err error)
}

// MongoDB is the MongoDB source.
type MongoDB struct {
	name  string
	label string
	api   mongoAPI
	ping  func(context.Context) error
	close func() error
	write bool
}

// OpenMongo opens rawURL, with database name database (empty: the URL's
// path), as the source called name. write lets aggregate use $out and $merge.
// Errors never quote the URL.
func OpenMongo(ctx context.Context, name, rawURL, database string, write bool) (*MongoDB, error) {
	var dbName []string
	if database != "" {
		dbName = []string{database}
	}

	c, err := vmongo.Open(ctx, rawURL, dbName...)
	if err != nil {
		return nil, fmt.Errorf("studio: cannot open MongoDB (%s): %w", Redact(rawURL), errNoURL(err, rawURL))
	}

	return &MongoDB{
		name: name, label: Redact(rawURL), api: realMongo{db: c.Database()}, write: write,
		ping:  func(ctx context.Context) error { return c.Raw().Ping(ctx, nil) },
		close: func() error { return c.Close(context.Background()) },
	}, nil
}

// Name implements Source.
func (m *MongoDB) Name() string { return m.name }

// Kind implements Source.
func (*MongoDB) Kind() Kind { return KindMongo }

// Label implements Source: host and database, never credentials.
func (m *MongoDB) Label() string { return m.label }

// Ping implements Source.
func (m *MongoDB) Ping(ctx context.Context) error { return m.ping(ctx) }

// Close implements Source.
func (m *MongoDB) Close() error { return m.close() }

// Collections implements MongoSource.
func (m *MongoDB) Collections(ctx context.Context) ([]Collection, error) {
	names, err := m.api.listCollections(ctx)
	if err != nil {
		return nil, err
	}

	slices.Sort(names)

	out := make([]Collection, 0, len(names))

	for _, n := range names {
		est, err := m.api.estimate(ctx, n)
		if err != nil {
			est = -1
		}

		out = append(out, Collection{Name: n, Estimate: est})
	}

	return out, nil
}

// known fails closed for a collection the database does not list.
func (m *MongoDB) known(ctx context.Context, coll string) error {
	names, err := m.api.listCollections(ctx)
	if err != nil {
		return err
	}

	if !slices.Contains(names, coll) {
		return ErrNotFound
	}

	return nil
}

func perPage(n int) int {
	if n < 1 {
		return 50
	}

	return min(n, 200)
}

// Documents implements MongoSource: filtered, sorted and paged on the server.
// Ordering by _id pages by keyset; another field pages by skip (capped).
func (m *MongoDB) Documents(ctx context.Context, q DocQuery) (DocPage, error) {
	if err := m.known(ctx, q.Collection); err != nil {
		return DocPage{}, err
	}

	filter, err := buildMongoFilter(q.Filters)
	if err != nil {
		return DocPage{}, err
	}

	n := perPage(q.PerPage)
	dir := int32(1)

	if q.Desc {
		dir = -1
	}

	var (
		skip int64
		sort bson.D
	)

	if q.Sort == "" || q.Sort == "_id" {
		sort = bson.D{{Key: "_id", Value: dir}}

		if q.After != "" {
			after, err := parseID(q.After)
			if err != nil {
				return DocPage{}, err
			}

			op := "$gt"
			if q.Desc {
				op = "$lt"
			}

			filter = andFilter(filter, bson.D{{Key: "_id", Value: bson.D{{Key: op, Value: after}}}})
		}
	} else {
		if !fieldRe.MatchString(q.Sort) {
			return DocPage{}, errors.New("studio: invalid sort field")
		}

		sort = bson.D{{Key: q.Sort, Value: dir}, {Key: "_id", Value: 1}}
		skip = int64(min(max(q.Page, 1), maxSkipPages)-1) * int64(n)
	}

	ctx, cancel := context.WithTimeout(ctx, mongoTimeout)
	defer cancel()

	raws, err := m.api.find(ctx, q.Collection, filter, sort, skip, int64(n)+1)
	if err != nil {
		return DocPage{}, err
	}

	page := DocPage{HasMore: len(raws) > n}
	if page.HasMore {
		raws = raws[:n]
	}

	if page, err = fillPage(page, raws); err != nil {
		return DocPage{}, err
	}

	if page.HasMore && len(page.Docs) > 0 && (q.Sort == "" || q.Sort == "_id") {
		page.Next = page.Docs[len(page.Docs)-1].ID
	}

	return page, nil
}

func andFilter(a, b bson.D) bson.D {
	if len(a) == 0 {
		return b
	}

	return bson.D{{Key: "$and", Value: bson.A{a, b}}}
}

func fillPage(page DocPage, raws []bson.Raw) (DocPage, error) {
	for _, raw := range raws {
		d, err := toDoc(raw)
		if err != nil {
			return page, err
		}

		page.Docs = append(page.Docs, d)
	}

	return page, nil
}

// toDoc renders a document: _id as canonical Extended JSON, the body as
// indented relaxed Extended JSON (cut at 256 KiB for display).
func toDoc(raw bson.Raw) (Doc, error) {
	var id any

	if v, err := raw.LookupErr("_id"); err == nil {
		if err := v.Unmarshal(&id); err != nil {
			return Doc{}, err
		}
	}

	text, err := idText(id)
	if err != nil {
		return Doc{}, err
	}

	b, err := bson.MarshalExtJSON(raw, false, false)
	if err != nil {
		return Doc{}, err
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b, "", "  "); err != nil {
		return Doc{}, err
	}

	s := pretty.String()
	if len(s) > maxDocDisplay {
		s = s[:maxDocDisplay] + "\n…"
	}

	return Doc{ID: text, JSON: s}, nil
}

// Document implements MongoSource.
func (m *MongoDB) Document(ctx context.Context, coll, id string) (Doc, error) {
	if err := m.known(ctx, coll); err != nil {
		return Doc{}, err
	}

	v, err := parseID(id)
	if err != nil {
		return Doc{}, err
	}

	raw, err := m.api.findID(ctx, coll, v)
	if err != nil {
		return Doc{}, err
	}

	return toDoc(raw)
}

// Find implements MongoSource: the console's find, with the typed filter
// checked first (no server-side JavaScript).
func (m *MongoDB) Find(ctx context.Context, coll, filterJSON string) (DocPage, error) {
	if err := m.known(ctx, coll); err != nil {
		return DocPage{}, err
	}

	filter := bson.D{}

	if filterJSON != "" {
		var err error
		if filter, err = decodeConsoleDoc(filterJSON); err != nil {
			return DocPage{}, err
		}
	}

	if err := checkConsoleValue(filter, m.write); err != nil {
		return DocPage{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, mongoTimeout)
	defer cancel()

	raws, err := m.api.find(ctx, coll, filter, bson.D{{Key: "_id", Value: 1}}, 0, maxConsoleDoc+1)
	if err != nil {
		return DocPage{}, err
	}

	return consolePage(raws)
}

// Aggregate implements MongoSource: the console's aggregate. $where,
// $function and $accumulator are refused always; $out and $merge without
// --write.
func (m *MongoDB) Aggregate(ctx context.Context, coll, pipelineJSON string) (DocPage, error) {
	if err := m.known(ctx, coll); err != nil {
		return DocPage{}, err
	}

	stages, err := decodePipeline(pipelineJSON)
	if err != nil {
		return DocPage{}, err
	}

	if err := checkConsoleValue(stages, m.write); err != nil {
		return DocPage{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, mongoTimeout)
	defer cancel()

	raws, err := m.api.aggregate(ctx, coll, stages, maxConsoleDoc+1)
	if err != nil {
		return DocPage{}, err
	}

	return consolePage(raws)
}

func consolePage(raws []bson.Raw) (DocPage, error) {
	page := DocPage{HasMore: len(raws) > maxConsoleDoc}
	if page.HasMore {
		raws = raws[:maxConsoleDoc]
	}

	return fillPage(page, raws)
}

// Insert implements MongoSource (--write only).
func (m *MongoDB) Insert(ctx context.Context, coll, docJSON string) error {
	if err := m.known(ctx, coll); err != nil {
		return err
	}

	doc, err := decodeConsoleDoc(docJSON)
	if err != nil {
		return err
	}

	if err := checkConsoleValue(doc, false); err != nil {
		return err
	}

	return m.api.insert(ctx, coll, doc)
}

// Replace implements MongoSource (--write only): ReplaceOne by _id, which must
// match exactly one document. The body's _id, if any, must equal the route's.
func (m *MongoDB) Replace(ctx context.Context, coll, id, docJSON string) error {
	if err := m.known(ctx, coll); err != nil {
		return err
	}

	want, err := parseID(id)
	if err != nil {
		return err
	}

	doc, err := decodeConsoleDoc(docJSON)
	if err != nil {
		return err
	}

	if err := checkConsoleValue(doc, false); err != nil {
		return err
	}

	doc, err = sameID(doc, want)
	if err != nil {
		return err
	}

	n, err := m.api.replace(ctx, coll, want, doc)
	if err != nil {
		return err
	}

	if n != 1 {
		return fmt.Errorf("studio: expected 1 document, matched %d", n)
	}

	return nil
}

// sameID makes sure the body carries the route's _id: equal when present,
// added when absent.
func sameID(doc bson.D, want any) (bson.D, error) {
	for _, e := range doc {
		if e.Key == "_id" {
			if !reflect.DeepEqual(e.Value, want) {
				return nil, errors.New("studio: the _id in the body differs from the document's _id")
			}

			return doc, nil
		}
	}

	return append(bson.D{{Key: "_id", Value: want}}, doc...), nil
}

// Delete implements MongoSource (--write only): DeleteOne by _id.
func (m *MongoDB) Delete(ctx context.Context, coll, id string) error {
	if err := m.known(ctx, coll); err != nil {
		return err
	}

	v, err := parseID(id)
	if err != nil {
		return err
	}

	n, err := m.api.remove(ctx, coll, v)
	if err != nil {
		return err
	}

	if n != 1 {
		return fmt.Errorf("studio: expected 1 document, deleted %d", n)
	}

	return nil
}

// realMongo is mongoAPI over a driver database.
type realMongo struct{ db *mongo.Database }

func (r realMongo) listCollections(ctx context.Context) ([]string, error) {
	return r.db.ListCollectionNames(ctx, bson.D{})
}

func (r realMongo) estimate(ctx context.Context, coll string) (int64, error) {
	return r.db.Collection(coll).EstimatedDocumentCount(ctx)
}

func (r realMongo) find(ctx context.Context, coll string, filter, sort bson.D, skip, limit int64) ([]bson.Raw, error) {
	cur, err := r.db.Collection(coll).Find(ctx, filter, options.Find().SetSort(sort).SetSkip(skip).SetLimit(limit))
	if err != nil {
		return nil, err
	}

	return drain(ctx, cur)
}

func (r realMongo) findID(ctx context.Context, coll string, id any) (bson.Raw, error) {
	raw, err := r.db.Collection(coll).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}

	return raw, err
}

func (r realMongo) aggregate(ctx context.Context, coll string, pipeline bson.A, limit int64) ([]bson.Raw, error) {
	cur, err := r.db.Collection(coll).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}

	return drainN(ctx, cur, limit)
}

func (r realMongo) insert(ctx context.Context, coll string, doc bson.D) error {
	_, err := r.db.Collection(coll).InsertOne(ctx, doc)
	return err
}

func (r realMongo) replace(ctx context.Context, coll string, id any, doc bson.D) (int64, error) {
	res, err := r.db.Collection(coll).ReplaceOne(ctx, bson.D{{Key: "_id", Value: id}}, doc)
	if err != nil {
		return 0, err
	}

	return res.MatchedCount, nil
}

func (r realMongo) remove(ctx context.Context, coll string, id any) (int64, error) {
	res, err := r.db.Collection(coll).DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return 0, err
	}

	return res.DeletedCount, nil
}

func drain(ctx context.Context, cur *mongo.Cursor) ([]bson.Raw, error) {
	return drainN(ctx, cur, 1<<30)
}

// drainN reads at most limit documents and closes the cursor.
func drainN(ctx context.Context, cur *mongo.Cursor, limit int64) ([]bson.Raw, error) {
	defer cur.Close(ctx)

	var out []bson.Raw

	for int64(len(out)) < limit && cur.Next(ctx) {
		out = append(out, slices.Clone(cur.Current))
	}

	return out, cur.Err()
}
