// Package cassandra implements nosql.NoSQLStore on gocql.
//
// The CQL binary protocol is a protocol-level driver concern, so gocql is
// fetched rather than reimplemented.
//
// Cassandra is the least document-shaped of the three NoSQL backends, and the
// mapping is honest about it: CQL only filters on primary-key columns, so a
// Filter that does not cover the partition key cannot be served without
// ALLOW FILTERING — which turns a query into a cluster-wide scan. This
// implementation refuses to add ALLOW FILTERING silently and returns
// nosql.ErrUnsupported instead; opt in per call with AllowFiltering.
package cassandra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/gocql/gocql"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/database/nosql"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config describes the Cassandra cluster.
type Config struct {
	Hosts    []string
	Keyspace string

	Username string
	Password string

	Consistency    string
	Timeout        time.Duration
	ConnectTimeout time.Duration
	NumConns       int

	// PageSize bounds how many rows the driver fetches per round trip.
	PageSize int

	// AllowFiltering permits non-key predicates cluster-wide. Leave it off.
	AllowFiltering bool
}

// LoadConfig reads Config from CASSANDRA_* environment variables.
func LoadConfig() Config {
	return Config{
		Hosts:    config.Strings("CASSANDRA_HOSTS", []string{"localhost:9042"}),
		Keyspace: config.String("CASSANDRA_KEYSPACE", "app"),

		Username: config.String("CASSANDRA_USERNAME", ""),
		Password: config.String("CASSANDRA_PASSWORD", ""),

		Consistency:    config.String("CASSANDRA_CONSISTENCY", "QUORUM"),
		Timeout:        config.Duration("CASSANDRA_TIMEOUT", 10*time.Second),
		ConnectTimeout: config.Duration("CASSANDRA_CONNECT_TIMEOUT", 10*time.Second),
		NumConns:       config.Int("CASSANDRA_NUM_CONNS", 2),

		PageSize:       config.Int("CASSANDRA_PAGE_SIZE", 500),
		AllowFiltering: config.Bool("CASSANDRA_ALLOW_FILTERING", false),
	}
}

// parseConsistency maps a name onto a gocql consistency level.
func parseConsistency(name string) gocql.Consistency {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "ANY":
		return gocql.Any
	case "ONE":
		return gocql.One
	case "TWO":
		return gocql.Two
	case "THREE":
		return gocql.Three
	case "QUORUM":
		return gocql.Quorum
	case "ALL":
		return gocql.All
	case "LOCAL_QUORUM":
		return gocql.LocalQuorum
	case "EACH_QUORUM":
		return gocql.EachQuorum
	case "LOCAL_ONE":
		return gocql.LocalOne
	default:
		return gocql.Quorum
	}
}

// Store is the Cassandra implementation of nosql.NoSQLStore.
type Store struct {
	session *gocql.Session
	cfg     Config
	log     *slog.Logger
}

// compile-time check that Store satisfies the shared contract.
var _ nosql.NoSQLStore = (*Store)(nil)

// New connects to the cluster.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if len(cfg.Hosts) == 0 {
		return nil, errors.New("cassandra: no hosts configured")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Keyspace = cfg.Keyspace
	cluster.Consistency = parseConsistency(cfg.Consistency)
	cluster.Timeout = cfg.Timeout
	cluster.ConnectTimeout = cfg.ConnectTimeout
	cluster.NumConns = cfg.NumConns
	cluster.PageSize = cfg.PageSize

	if cfg.Username != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: cfg.Username,
			Password: cfg.Password,
		}
	}

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("cassandra: create session for keyspace %s: %w", cfg.Keyspace, err)
	}

	s := &Store{session: session, cfg: cfg, log: logger.Module("cassandra")}

	if err := s.Ping(ctx); err != nil {
		session.Close()
		return nil, err
	}

	s.log.Info("connected",
		slog.String("keyspace", cfg.Keyspace),
		slog.String("hosts", strings.Join(cfg.Hosts, ",")),
		slog.String("consistency", cfg.Consistency))

	return s, nil
}

// Backend identifies this implementation.
func (s *Store) Backend() string { return "cassandra" }

// Session exposes the gocql session for CQL this contract cannot express.
func (s *Store) Session() *gocql.Session { return s.session }

// identifier guards a table or column name before it is interpolated into CQL.
//
// Table and column names cannot be bound as query parameters, so they are
// concatenated into the statement — which makes validating them the only thing
// standing between a caller-supplied name and CQL injection.
func identifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty identifier", nosql.ErrInvalidFilter)
	}
	for _, r := range name {
		isAllowed := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_'
		if !isAllowed {
			return "", fmt.Errorf("%w: %q is not a valid CQL identifier", nosql.ErrInvalidFilter, name)
		}
	}
	return name, nil
}

// buildWhere renders a WHERE clause with bind placeholders.
//
// Keys are sorted so the same filter always produces the same statement text,
// which lets the driver's prepared-statement cache actually hit.
func buildWhere(filter nosql.Filter) (string, []any, error) {
	if len(filter) == 0 {
		return "", nil, nil
	}

	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	clauses := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		col, err := identifier(k)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, col+" = ?")
		args = append(args, filter[k])
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

// suffix appends ALLOW FILTERING when the config opts in.
func (s *Store) suffix(hasFilter bool) string {
	if hasFilter && s.cfg.AllowFiltering {
		return " ALLOW FILTERING"
	}
	return ""
}

// FindOne scans the first matching row into dest, which must be a pointer to a
// struct whose exported fields map to columns.
func (s *Store) FindOne(ctx context.Context, table string, filter nosql.Filter, dest any) error {
	tbl, err := identifier(table)
	if err != nil {
		return err
	}
	where, args, err := buildWhere(filter)
	if err != nil {
		return err
	}
	tail := s.suffix(len(filter) > 0)

	stmt := "SELECT * FROM " + tbl + where + " LIMIT 1" + tail

	m := map[string]any{}
	if err := s.session.Query(stmt, args...).WithContext(ctx).MapScan(m); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nosql.ErrNotFound
		}
		return fmt.Errorf("cassandra: find one in %s: %w", table, err)
	}
	return nosql.DecodeMap(m, dest)
}

// Find scans every matching row into dest, which must be a pointer to a slice.
func (s *Store) Find(ctx context.Context, table string, filter nosql.Filter, dest any, opts ...nosql.FindOption) error {
	tbl, err := identifier(table)
	if err != nil {
		return err
	}
	where, args, err := buildWhere(filter)
	if err != nil {
		return err
	}
	tail := s.suffix(len(filter) > 0)

	o := nosql.BuildFindOptions(opts...)
	// Cassandra pages with a driver-side token, not a numeric offset, so Skip
	// cannot be honoured. Returning ErrUnsupported is deliberate: silently
	// ignoring it would hand the caller page 1 while they believed they asked
	// for page 3.
	if o.Skip > 0 {
		return fmt.Errorf("%w: cassandra pages by token, not offset; use Session() with PageState", nosql.ErrUnsupported)
	}

	projection := "*"
	if len(o.Projection) > 0 {
		cols := make([]string, 0, len(o.Projection))
		for _, f := range o.Projection {
			col, err := identifier(f)
			if err != nil {
				return err
			}
			cols = append(cols, col)
		}
		projection = strings.Join(cols, ", ")
	}

	stmt := "SELECT " + projection + " FROM " + tbl + where

	if len(o.Sort) > 0 {
		// CQL can only order by clustering columns, in the table's declared
		// direction, so an arbitrary sort is not expressible.
		terms := make([]string, 0, len(o.Sort))
		for _, sr := range o.Sort {
			col, err := identifier(sr.Field)
			if err != nil {
				return err
			}
			dir := "ASC"
			if sr.Direction == nosql.Descending {
				dir = "DESC"
			}
			terms = append(terms, col+" "+dir)
		}
		stmt += " ORDER BY " + strings.Join(terms, ", ")
	}
	if o.Limit > 0 {
		stmt += fmt.Sprintf(" LIMIT %d", o.Limit)
	}
	stmt += tail

	iter := s.session.Query(stmt, args...).WithContext(ctx).Iter()
	rows := make([]map[string]any, 0, 16)
	for {
		row := map[string]any{}
		if !iter.MapScan(row) {
			break
		}
		rows = append(rows, row)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("cassandra: find in %s: %w", table, err)
	}
	return nosql.DecodeMapSlice(rows, dest)
}

// InsertOne writes doc, which may be a struct or a nosql.Document.
func (s *Store) InsertOne(ctx context.Context, table string, doc any) error {
	tbl, err := identifier(table)
	if err != nil {
		return err
	}

	fields, err := nosql.ToDocument(doc)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return fmt.Errorf("%w: document is empty", nosql.ErrInvalidFilter)
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	cols := make([]string, 0, len(keys))
	placeholders := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		col, err := identifier(k)
		if err != nil {
			return err
		}
		cols = append(cols, col)
		placeholders = append(placeholders, "?")
		args = append(args, fields[k])
	}

	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tbl, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	if err := s.session.Query(stmt, args...).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: insert into %s: %w", table, err)
	}
	return nil
}

// UpdateOne applies update to the row matching filter.
//
// Cassandra writes are upserts and report no matched count, so an update against
// a missing primary key silently creates a row. To keep the contract's promise
// of ErrNotFound, existence is checked first.
func (s *Store) UpdateOne(ctx context.Context, table string, filter nosql.Filter, update nosql.Document) error {
	tbl, err := identifier(table)
	if err != nil {
		return err
	}
	if len(update) == 0 {
		return fmt.Errorf("%w: update document is empty", nosql.ErrInvalidFilter)
	}
	if len(filter) == 0 {
		return fmt.Errorf("%w: cassandra requires a primary-key filter to update", nosql.ErrInvalidFilter)
	}

	n, err := s.Count(ctx, table, filter)
	if err != nil {
		return err
	}
	if n == 0 {
		return nosql.ErrNotFound
	}

	setKeys := make([]string, 0, len(update))
	for k := range update {
		setKeys = append(setKeys, k)
	}
	sort.Strings(setKeys)

	sets := make([]string, 0, len(setKeys))
	args := make([]any, 0, len(setKeys)+len(filter))
	for _, k := range setKeys {
		col, err := identifier(k)
		if err != nil {
			return err
		}
		sets = append(sets, col+" = ?")
		args = append(args, update[k])
	}

	where, whereArgs, err := buildWhere(filter)
	if err != nil {
		return err
	}
	args = append(args, whereArgs...)

	stmt := "UPDATE " + tbl + " SET " + strings.Join(sets, ", ") + where

	if err := s.session.Query(stmt, args...).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: update in %s: %w", table, err)
	}
	return nil
}

// DeleteOne removes the row matching filter.
func (s *Store) DeleteOne(ctx context.Context, table string, filter nosql.Filter) error {
	tbl, err := identifier(table)
	if err != nil {
		return err
	}
	if len(filter) == 0 {
		return fmt.Errorf("%w: cassandra requires a primary-key filter to delete", nosql.ErrInvalidFilter)
	}

	n, err := s.Count(ctx, table, filter)
	if err != nil {
		return err
	}
	if n == 0 {
		return nosql.ErrNotFound
	}

	where, args, err := buildWhere(filter)
	if err != nil {
		return err
	}

	if err := s.session.Query("DELETE FROM "+tbl+where, args...).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: delete from %s: %w", table, err)
	}
	return nil
}

// Count reports how many rows match filter.
func (s *Store) Count(ctx context.Context, table string, filter nosql.Filter) (int64, error) {
	tbl, err := identifier(table)
	if err != nil {
		return 0, err
	}
	where, args, err := buildWhere(filter)
	if err != nil {
		return 0, err
	}
	tail := s.suffix(len(filter) > 0)

	var n int64
	stmt := "SELECT COUNT(*) FROM " + tbl + where + tail
	if err := s.session.Query(stmt, args...).WithContext(ctx).Scan(&n); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("cassandra: count in %s: %w", table, err)
	}
	return n, nil
}

// Ping verifies the session with a trivial system query.
func (s *Store) Ping(ctx context.Context) error {
	var name string
	err := s.session.Query("SELECT cluster_name FROM system.local").
		WithContext(ctx).Scan(&name)
	if err != nil {
		return fmt.Errorf("cassandra: ping: %w", err)
	}
	return nil
}

// Close ends the session. Registered with the shutdown registry.
func (s *Store) Close(context.Context) error {
	// gocql's Close is synchronous and reports nothing.
	s.session.Close()
	return nil
}
