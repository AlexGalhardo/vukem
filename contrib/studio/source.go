package studio

import "context"

// Kind is the family of a data source.
type Kind string

// The families Studio knows. Only SQL has a source today.
const (
	KindSQL Kind = "sql"
)

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
