package studio

import (
	"context"
	"time"
)

// Kind is the family of a data source.
type Kind string

// KindSQL is the relational family.
const KindSQL Kind = "sql"

// Source is one configured data source.
type Source interface {
	// Name is the URL-safe id ("db").
	Name() string
	Kind() Kind
	// Label is safe to show: driver, host and database, never credentials.
	Label() string
	Ping(ctx context.Context) error
	Close() error
}

// KindMongo and KindRedis are the other families of data source.
const (
	KindMongo Kind = "mongo"
	KindRedis Kind = "redis"
)

// Filter is one condition of a grid, shared by the SQL, MongoDB and Redis
// browsers: a column (a field path for MongoDB), an operator and a value.
// Operators: eq, ne, lt, lte, gt, gte, contains, starts, null, notnull.
type Filter struct {
	Column string
	Op     string
	Value  string
}

// MongoSource is a MongoDB source.
type MongoSource interface {
	Source
	Collections(ctx context.Context) ([]Collection, error)
	Documents(ctx context.Context, q DocQuery) (DocPage, error)
	Document(ctx context.Context, collection, id string) (Doc, error)
	// Find and Aggregate are the console: Extended JSON typed by the developer.
	Find(ctx context.Context, collection, filterJSON string) (DocPage, error)
	Aggregate(ctx context.Context, collection, pipelineJSON string) (DocPage, error)
	// Writes, used only when Studio runs with --write.
	Insert(ctx context.Context, collection, docJSON string) error
	Replace(ctx context.Context, collection, id, docJSON string) error
	Delete(ctx context.Context, collection, id string) error
}

// Collection is a MongoDB collection.
type Collection struct {
	Name     string
	Estimate int64 // estimated count; -1 when unknown
}

// DocQuery selects a page of documents.
type DocQuery struct {
	Collection string
	Filters    []Filter
	Sort       string // field path; empty means _id
	Desc       bool
	After      string // keyset cursor: the _id of the last row, as Extended JSON
	Page       int    // 1-based, used only when Sort is not _id (capped at 1000)
	PerPage    int    // 1..200, default 50
}

// Doc is one document.
type Doc struct {
	ID   string // _id as canonical Extended JSON
	JSON string // the document as indented relaxed Extended JSON
}

// DocPage is a page of documents.
type DocPage struct {
	Docs    []Doc
	Next    string
	HasMore bool
}

// RedisSource is a Redis source.
type RedisSource interface {
	Source
	Keys(ctx context.Context, q KeyQuery) (KeyPage, error)
	Key(ctx context.Context, key string, page int) (KeyDetail, error)
	// Command is the console: one allowed command line.
	Command(ctx context.Context, line string, write bool) (string, error)
	// Writes, used only when Studio runs with --write.
	SetString(ctx context.Context, key, value string) error
	SetField(ctx context.Context, key, field, value string) error
	SetTTL(ctx context.Context, key string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// KeyQuery is one SCAN step.
type KeyQuery struct {
	Match  string // glob pattern; empty means "*"
	Cursor uint64
	Count  int // SCAN hint, 1..1000, default 200
}

// KeyInfo describes a key.
type KeyInfo struct {
	Key  string
	Type string        // string, hash, list, set, zset, stream
	TTL  time.Duration // -1 when the key does not expire
}

// KeyPage is a page of keys.
type KeyPage struct {
	Keys   []KeyInfo
	Cursor uint64 // 0 when the scan is complete
}

// KeyDetail is the value of a key.
type KeyDetail struct {
	KeyInfo
	Size   int64       // elements (bytes for a string)
	Fields [][2]string // (field, value), (index, value), (member, score) or (id, body)
	More   bool        // more elements exist beyond this page
}
