// Package mongo implements nosql.NoSQLStore on the official MongoDB driver.
//
// The wire protocol is far too involved to reimplement, so the driver is
// fetched; this file exists to hold configuration and to translate the driver's
// vocabulary into the shared contract.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/database/nosql"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config describes the MongoDB connection.
type Config struct {
	URI      string
	Database string

	ConnectTimeout time.Duration
	QueryTimeout   time.Duration

	MaxPoolSize     uint64
	MinPoolSize     uint64
	MaxConnIdleTime time.Duration
}

// LoadConfig reads Config from MONGO_* environment variables.
func LoadConfig() Config {
	return Config{
		URI:      config.String("MONGO_URI", "mongodb://localhost:27017"),
		Database: config.String("MONGO_DATABASE", "app"),

		ConnectTimeout: config.Duration("MONGO_CONNECT_TIMEOUT", 10*time.Second),
		QueryTimeout:   config.Duration("MONGO_QUERY_TIMEOUT", 10*time.Second),

		// max(0, ...) before the conversion: a negative value would wrap to an
		// enormous pool size rather than being rejected.
		MaxPoolSize:     uint64(max(0, config.Int("MONGO_MAX_POOL_SIZE", 100))),
		MinPoolSize:     uint64(max(0, config.Int("MONGO_MIN_POOL_SIZE", 5))),
		MaxConnIdleTime: config.Duration("MONGO_MAX_CONN_IDLE_TIME", 5*time.Minute),
	}
}

// Store is the MongoDB implementation of nosql.NoSQLStore.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
	cfg    Config
	log    *slog.Logger
}

// compile-time check that Store satisfies the shared contract.
var _ nosql.NoSQLStore = (*Store)(nil)

// New connects and verifies the connection.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 10 * time.Second
	}

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	opts := options.Client().
		ApplyURI(cfg.URI).
		SetMaxPoolSize(cfg.MaxPoolSize).
		SetMinPoolSize(cfg.MinPoolSize).
		SetMaxConnIdleTime(cfg.MaxConnIdleTime).
		SetConnectTimeout(cfg.ConnectTimeout)

	client, err := mongo.Connect(connectCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	s := &Store{
		client: client,
		db:     client.Database(cfg.Database),
		cfg:    cfg,
		log:    logger.Module("mongo"),
	}

	// mongo.Connect does not contact the server, so a bad URI surfaces only on
	// first use; ping to fail at boot instead.
	if err := s.Ping(connectCtx); err != nil {
		// WithoutCancel, not Background: keep the parent's values (deadlines
		// aside) while ignoring the cancellation that may have just fired.
		_ = client.Disconnect(context.WithoutCancel(ctx))
		return nil, err
	}

	s.log.Info("connected",
		slog.String("database", cfg.Database),
		slog.Uint64("max_pool_size", cfg.MaxPoolSize))

	return s, nil
}

// Backend identifies this implementation.
func (s *Store) Backend() string { return "mongo" }

// Database exposes the driver handle for operations outside the shared contract,
// such as an aggregation pipeline or a multi-document transaction.
func (s *Store) Database() *mongo.Database { return s.db }

// Client exposes the underlying driver client.
func (s *Store) Client() *mongo.Client { return s.client }

// withTimeout applies the configured query timeout unless the caller set a
// shorter deadline of their own.
func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || s.cfg.QueryTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.QueryTimeout)
}

// toBSON converts a shared Filter into a BSON document.
func toBSON(filter nosql.Filter) bson.M {
	out := bson.M{}
	for k, v := range filter {
		out[k] = v
	}
	return out
}

// FindOne decodes the single matching document into dest.
func (s *Store) FindOne(ctx context.Context, collection string, filter nosql.Filter, dest any) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	err := s.db.Collection(collection).FindOne(ctx, toBSON(filter)).Decode(dest)
	if err != nil {
		// Translate the driver sentinel so callers compare against
		// nosql.ErrNotFound regardless of backend.
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nosql.ErrNotFound
		}
		return fmt.Errorf("mongo: find one in %s: %w", collection, err)
	}
	return nil
}

// Find decodes every matching document into dest, which must point to a slice.
func (s *Store) Find(ctx context.Context, collection string, filter nosql.Filter, dest any, opts ...nosql.FindOption) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	o := nosql.BuildFindOptions(opts...)
	findOpts := options.Find()
	if o.Limit > 0 {
		findOpts.SetLimit(o.Limit)
	}
	if o.Skip > 0 {
		findOpts.SetSkip(o.Skip)
	}
	if len(o.Sort) > 0 {
		sort := make(bson.D, 0, len(o.Sort))
		for _, sr := range o.Sort {
			sort = append(sort, bson.E{Key: sr.Field, Value: int(sr.Direction)})
		}
		findOpts.SetSort(sort)
	}
	if len(o.Projection) > 0 {
		projection := bson.M{}
		for _, f := range o.Projection {
			projection[f] = 1
		}
		findOpts.SetProjection(projection)
	}

	cursor, err := s.db.Collection(collection).Find(ctx, toBSON(filter), findOpts)
	if err != nil {
		return fmt.Errorf("mongo: find in %s: %w", collection, err)
	}
	// cursor.All closes the cursor, but Close on the error path is still needed
	// to release the server-side cursor.
	defer cursor.Close(ctx)

	if err := cursor.All(ctx, dest); err != nil {
		return fmt.Errorf("mongo: decode results from %s: %w", collection, err)
	}
	return nil
}

// InsertOne writes doc.
func (s *Store) InsertOne(ctx context.Context, collection string, doc any) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if _, err := s.db.Collection(collection).InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("mongo: insert into %s: %w", collection, err)
	}
	return nil
}

// UpdateOne applies update with $set to the single matching document.
func (s *Store) UpdateOne(ctx context.Context, collection string, filter nosql.Filter, update nosql.Document) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if len(update) == 0 {
		return fmt.Errorf("%w: update document is empty", nosql.ErrInvalidFilter)
	}

	set := bson.M{}
	for k, v := range update {
		set[k] = v
	}

	res, err := s.db.Collection(collection).UpdateOne(ctx, toBSON(filter), bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("mongo: update in %s: %w", collection, err)
	}
	if res.MatchedCount == 0 {
		return nosql.ErrNotFound
	}
	return nil
}

// DeleteOne removes the single matching document.
func (s *Store) DeleteOne(ctx context.Context, collection string, filter nosql.Filter) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	res, err := s.db.Collection(collection).DeleteOne(ctx, toBSON(filter))
	if err != nil {
		return fmt.Errorf("mongo: delete from %s: %w", collection, err)
	}
	if res.DeletedCount == 0 {
		return nosql.ErrNotFound
	}
	return nil
}

// Count reports how many documents match filter.
func (s *Store) Count(ctx context.Context, collection string, filter nosql.Filter) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	n, err := s.db.Collection(collection).CountDocuments(ctx, toBSON(filter))
	if err != nil {
		return 0, fmt.Errorf("mongo: count in %s: %w", collection, err)
	}
	return n, nil
}

// Ping verifies connectivity.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}
	return nil
}

// Close disconnects the client. Registered with the shutdown registry.
func (s *Store) Close(ctx context.Context) error {
	if err := s.client.Disconnect(ctx); err != nil {
		return fmt.Errorf("mongo: disconnect: %w", err)
	}
	return nil
}
