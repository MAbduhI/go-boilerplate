// Package nosql defines one storage contract that the Mongo, Cassandra and
// DynamoDB connectors all implement.
//
// The point is that a usecase depends on NoSQLStore and never on a driver, so
// swapping the backing store is a wiring change rather than a rewrite. The
// interface is deliberately narrow: it covers the document operations all three
// backends share and nothing else. A backend-specific capability — a Mongo
// aggregation pipeline, a DynamoDB transactional write — belongs on that
// backend's concrete type, reached by type assertion, not bolted onto this
// interface where two of three implementations would have to return
// ErrUnsupported.
package nosql

import (
	"context"
	"errors"
)

// Sentinel errors every implementation must return for these conditions, so
// callers can branch without knowing the backend.
var (
	// ErrNotFound reports that no document matched.
	ErrNotFound = errors.New("nosql: document not found")

	// ErrUnsupported reports that a backend cannot express the request — for
	// example an unkeyed scan on a store that requires a partition key.
	ErrUnsupported = errors.New("nosql: operation not supported by this backend")

	// ErrInvalidFilter reports a filter a backend cannot translate.
	ErrInvalidFilter = errors.New("nosql: invalid filter")
)

// Filter selects documents as field/value equality pairs, combined with AND.
//
// Equality-only is the deliberate lower bound of the three backends: Cassandra
// restricts predicates to the primary key and DynamoDB to key conditions, so a
// richer expression language here would not be portable. Reach for the
// concrete driver when a query needs more.
type Filter map[string]any

// Document is a set of field/value pairs used for writes and updates.
type Document map[string]any

// SortDirection orders a Find result.
type SortDirection int

const (
	Ascending  SortDirection = 1
	Descending SortDirection = -1
)

// Sort names one ordering term.
type Sort struct {
	Field     string
	Direction SortDirection
}

// FindOptions tunes a multi-document read. Backends apply what they support and
// ignore the rest rather than failing, except where the difference would change
// correctness.
type FindOptions struct {
	Limit      int64
	Skip       int64
	Sort       []Sort
	Projection []string
}

// FindOption mutates FindOptions.
type FindOption func(*FindOptions)

// WithLimit caps the number of documents returned.
func WithLimit(n int64) FindOption { return func(o *FindOptions) { o.Limit = n } }

// WithSkip discards the first n documents. Not all backends can express it;
// those that cannot return ErrUnsupported rather than quietly ignoring it,
// because a silently unpaginated result is a correctness bug.
func WithSkip(n int64) FindOption { return func(o *FindOptions) { o.Skip = n } }

// WithSort orders the result by the given terms.
func WithSort(sorts ...Sort) FindOption { return func(o *FindOptions) { o.Sort = sorts } }

// WithProjection restricts the fields returned.
func WithProjection(fields ...string) FindOption {
	return func(o *FindOptions) { o.Projection = fields }
}

// BuildFindOptions applies opts over the zero value.
func BuildFindOptions(opts ...FindOption) FindOptions {
	var o FindOptions
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}

// NoSQLStore is the document-store contract shared by every NoSQL connector.
//
// collection names the Mongo collection, Cassandra table or DynamoDB table.
// dest is a pointer to a struct (FindOne) or to a slice of structs (Find),
// decoded using each driver's own tags.
type NoSQLStore interface {
	// FindOne decodes the single document matching filter into dest, returning
	// ErrNotFound when there is none.
	FindOne(ctx context.Context, collection string, filter Filter, dest any) error

	// Find decodes every document matching filter into dest, which must point to
	// a slice. An empty result is not an error.
	Find(ctx context.Context, collection string, filter Filter, dest any, opts ...FindOption) error

	// InsertOne writes doc, which may be a struct or a Document.
	InsertOne(ctx context.Context, collection string, doc any) error

	// UpdateOne applies update to the single document matching filter, returning
	// ErrNotFound when there is none.
	UpdateOne(ctx context.Context, collection string, filter Filter, update Document) error

	// DeleteOne removes the single document matching filter, returning
	// ErrNotFound when there is none.
	DeleteOne(ctx context.Context, collection string, filter Filter) error

	// Count reports how many documents match filter.
	Count(ctx context.Context, collection string, filter Filter) (int64, error)

	// Ping verifies connectivity, for health checks.
	Ping(ctx context.Context) error

	// Close releases the connection. Registered with the shutdown registry.
	Close(ctx context.Context) error

	// Backend names the implementation ("mongo", "cassandra", "dynamodb") for
	// logs and health output.
	Backend() string
}
