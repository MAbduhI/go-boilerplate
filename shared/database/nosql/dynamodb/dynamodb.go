// Package dynamodb implements nosql.NoSQLStore on the official AWS SDK v2.
//
// The SDK is fetched rather than reimplemented: DynamoDB requires SigV4 request
// signing and credential-chain resolution, and hand-rolling either is a
// security mistake, not an optimisation.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/database/nosql"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config describes the DynamoDB client.
type Config struct {
	Region string

	// Endpoint points at DynamoDB Local for development. Leave it empty to use
	// the real AWS endpoint for Region.
	Endpoint string

	// Static credentials are for local development only; in AWS leave these
	// empty so the default credential chain (IRSA, instance role, SSO) applies.
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	Timeout time.Duration

	// TablePrefix is prepended to every table name, which is how one account
	// hosts several environments.
	TablePrefix string
}

// LoadConfig reads Config from DYNAMODB_* environment variables.
func LoadConfig() Config {
	return Config{
		Region:   config.String("DYNAMODB_REGION", "ap-southeast-1"),
		Endpoint: config.String("DYNAMODB_ENDPOINT", ""),

		AccessKeyID:     config.String("DYNAMODB_ACCESS_KEY_ID", ""),
		SecretAccessKey: config.String("DYNAMODB_SECRET_ACCESS_KEY", ""),
		SessionToken:    config.String("DYNAMODB_SESSION_TOKEN", ""),

		Timeout:     config.Duration("DYNAMODB_TIMEOUT", 10*time.Second),
		TablePrefix: config.String("DYNAMODB_TABLE_PREFIX", ""),
	}
}

// Store is the DynamoDB implementation of nosql.NoSQLStore.
type Store struct {
	client *ddb.Client
	cfg    Config
	log    *slog.Logger
}

// compile-time check that Store satisfies the shared contract.
var _ nosql.NoSQLStore = (*Store)(nil)

// New builds the client from the AWS default config chain.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			staticCredentials{cfg: cfg},
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: load AWS config: %w", err)
	}

	clientOpts := []func(*ddb.Options){}
	if cfg.Endpoint != "" {
		endpoint := cfg.Endpoint
		clientOpts = append(clientOpts, func(o *ddb.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}

	s := &Store{
		client: ddb.NewFromConfig(awsCfg, clientOpts...),
		cfg:    cfg,
		log:    logger.Module("dynamodb"),
	}

	if err := s.Ping(ctx); err != nil {
		return nil, err
	}

	s.log.Info("connected",
		slog.String("region", cfg.Region),
		slog.String("endpoint", orDefault(cfg.Endpoint, "aws")),
		slog.String("table_prefix", cfg.TablePrefix))

	return s, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// staticCredentials serves credentials from config for local development.
type staticCredentials struct{ cfg Config }

func (s staticCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     s.cfg.AccessKeyID,
		SecretAccessKey: s.cfg.SecretAccessKey,
		SessionToken:    s.cfg.SessionToken,
		Source:          "shared/database/nosql/dynamodb",
	}, nil
}

// Backend identifies this implementation.
func (s *Store) Backend() string { return "dynamodb" }

// Client exposes the SDK client for operations outside the shared contract,
// such as Query on a secondary index or a transactional write.
func (s *Store) Client() *ddb.Client { return s.client }

// table applies the configured prefix.
func (s *Store) table(name string) *string {
	return aws.String(s.cfg.TablePrefix + name)
}

func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || s.cfg.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.Timeout)
}

// toAttrMap marshals a filter or document into DynamoDB attribute values.
func toAttrMap(in map[string]any) (map[string]types.AttributeValue, error) {
	out, err := attributevalue.MarshalMap(in)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: marshal attributes: %w", err)
	}
	return out, nil
}

// sortedKeys keeps generated expressions stable across calls.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildFilterExpression renders a filter as a DynamoDB filter expression with
// placeholder names and values.
//
// Attribute names go through #n0-style placeholders because many useful names
// collide with DynamoDB's long list of reserved words, and a literal name would
// fail at request time.
func buildFilterExpression(filter nosql.Filter) (expr string, names map[string]string, values map[string]types.AttributeValue, err error) {
	if len(filter) == 0 {
		return "", nil, nil, nil
	}

	names = map[string]string{}
	rawValues := map[string]any{}
	clauses := make([]string, 0, len(filter))

	for i, k := range sortedKeys(filter) {
		nameKey := fmt.Sprintf("#n%d", i)
		valueKey := fmt.Sprintf(":v%d", i)
		names[nameKey] = k
		rawValues[valueKey] = filter[k]
		clauses = append(clauses, nameKey+" = "+valueKey)
	}

	values, err = toAttrMap(rawValues)
	if err != nil {
		return "", nil, nil, err
	}
	return strings.Join(clauses, " AND "), names, values, nil
}

// FindOne returns the first matching item.
//
// This runs a Scan with a filter rather than a GetItem: the shared Filter does
// not tell us which attributes form the primary key. When the key is known,
// prefer Client().GetItem, which is a single-partition read instead of a table
// scan.
func (s *Store) FindOne(ctx context.Context, table string, filter nosql.Filter, dest any) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	expr, names, values, err := buildFilterExpression(filter)
	if err != nil {
		return err
	}

	in := &ddb.ScanInput{TableName: s.table(table), Limit: aws.Int32(1)}
	if expr != "" {
		in.FilterExpression = aws.String(expr)
		in.ExpressionAttributeNames = names
		in.ExpressionAttributeValues = values
	}

	out, err := s.client.Scan(ctx, in)
	if err != nil {
		return fmt.Errorf("dynamodb: scan %s: %w", table, err)
	}
	if len(out.Items) == 0 {
		return nosql.ErrNotFound
	}
	if err := attributevalue.UnmarshalMap(out.Items[0], dest); err != nil {
		return fmt.Errorf("dynamodb: unmarshal item from %s: %w", table, err)
	}
	return nil
}

// Find returns every matching item.
func (s *Store) Find(ctx context.Context, table string, filter nosql.Filter, dest any, opts ...nosql.FindOption) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	o := nosql.BuildFindOptions(opts...)
	// DynamoDB paginates with an exclusive start key, not a numeric offset, so
	// Skip is not expressible. Reporting it beats returning the wrong page.
	if o.Skip > 0 {
		return fmt.Errorf("%w: dynamodb paginates by LastEvaluatedKey, not offset; use Client().Scan directly", nosql.ErrUnsupported)
	}
	// Sorting happens on the sort key within a partition, which a table-wide
	// scan does not have.
	if len(o.Sort) > 0 {
		return fmt.Errorf("%w: dynamodb orders by sort key within a partition; use Client().Query", nosql.ErrUnsupported)
	}

	expr, names, values, err := buildFilterExpression(filter)
	if err != nil {
		return err
	}

	in := &ddb.ScanInput{TableName: s.table(table)}
	if expr != "" {
		in.FilterExpression = aws.String(expr)
		in.ExpressionAttributeNames = names
		in.ExpressionAttributeValues = values
	}
	if o.Limit > 0 {
		// DynamoDB's Limit is an int32; clamp rather than let the conversion wrap
		// a large int64 into a negative page size. min() bounds the value at
		// MaxInt32, so the conversion cannot overflow.
		limit := min(o.Limit, math.MaxInt32)
		in.Limit = aws.Int32(int32(limit)) //nolint:gosec // G115: bounded by min() above
	}
	if len(o.Projection) > 0 {
		if in.ExpressionAttributeNames == nil {
			in.ExpressionAttributeNames = map[string]string{}
		}
		placeholders := make([]string, 0, len(o.Projection))
		for i, f := range o.Projection {
			key := fmt.Sprintf("#p%d", i)
			in.ExpressionAttributeNames[key] = f
			placeholders = append(placeholders, key)
		}
		in.ProjectionExpression = aws.String(strings.Join(placeholders, ", "))
	}

	items := make([]map[string]types.AttributeValue, 0, 32)
	for {
		out, err := s.client.Scan(ctx, in)
		if err != nil {
			return fmt.Errorf("dynamodb: scan %s: %w", table, err)
		}
		items = append(items, out.Items...)

		// Stop once the requested limit is satisfied or the scan is exhausted.
		// A Scan returns at most 1 MB per call, so following
		// LastEvaluatedKey is required for a complete result.
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		if o.Limit > 0 && int64(len(items)) >= o.Limit {
			items = items[:o.Limit]
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}

	if err := attributevalue.UnmarshalListOfMaps(items, dest); err != nil {
		return fmt.Errorf("dynamodb: unmarshal items from %s: %w", table, err)
	}
	return nil
}

// InsertOne writes doc.
func (s *Store) InsertOne(ctx context.Context, table string, doc any) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	item, err := attributevalue.MarshalMap(doc)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal item for %s: %w", table, err)
	}

	_, err = s.client.PutItem(ctx, &ddb.PutItemInput{
		TableName: s.table(table),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("dynamodb: put item into %s: %w", table, err)
	}
	return nil
}

// UpdateOne applies update to the item matching filter.
//
// filter is treated as the primary key: UpdateItem addresses exactly one item by
// key, and a condition that the item already exists is attached so a missing key
// reports ErrNotFound instead of silently creating a row.
func (s *Store) UpdateOne(ctx context.Context, table string, filter nosql.Filter, update nosql.Document) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if len(filter) == 0 {
		return fmt.Errorf("%w: dynamodb requires a key filter to update", nosql.ErrInvalidFilter)
	}
	if len(update) == 0 {
		return fmt.Errorf("%w: update document is empty", nosql.ErrInvalidFilter)
	}

	key, err := toAttrMap(filter)
	if err != nil {
		return err
	}

	names := map[string]string{}
	rawValues := map[string]any{}
	sets := make([]string, 0, len(update))
	for i, k := range sortedKeys(update) {
		nameKey := fmt.Sprintf("#u%d", i)
		valueKey := fmt.Sprintf(":u%d", i)
		names[nameKey] = k
		rawValues[valueKey] = update[k]
		sets = append(sets, nameKey+" = "+valueKey)
	}
	values, err := toAttrMap(rawValues)
	if err != nil {
		return err
	}

	// Require the key attribute to exist, turning an upsert into an update.
	firstKey := sortedKeys(filter)[0]
	names["#k0"] = firstKey

	_, err = s.client.UpdateItem(ctx, &ddb.UpdateItemInput{
		TableName:                 s.table(table),
		Key:                       key,
		UpdateExpression:          aws.String("SET " + strings.Join(sets, ", ")),
		ConditionExpression:       aws.String("attribute_exists(#k0)"),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return nosql.ErrNotFound
		}
		return fmt.Errorf("dynamodb: update item in %s: %w", table, err)
	}
	return nil
}

// DeleteOne removes the item matching filter, which is treated as the key.
func (s *Store) DeleteOne(ctx context.Context, table string, filter nosql.Filter) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if len(filter) == 0 {
		return fmt.Errorf("%w: dynamodb requires a key filter to delete", nosql.ErrInvalidFilter)
	}

	key, err := toAttrMap(filter)
	if err != nil {
		return err
	}

	firstKey := sortedKeys(filter)[0]
	_, err = s.client.DeleteItem(ctx, &ddb.DeleteItemInput{
		TableName:                s.table(table),
		Key:                      key,
		ConditionExpression:      aws.String("attribute_exists(#k0)"),
		ExpressionAttributeNames: map[string]string{"#k0": firstKey},
	})
	if err != nil {
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return nosql.ErrNotFound
		}
		return fmt.Errorf("dynamodb: delete item from %s: %w", table, err)
	}
	return nil
}

// Count reports how many items match filter.
//
// Scan with Select=COUNT still reads the table, so this is not a cheap
// operation on a large table.
func (s *Store) Count(ctx context.Context, table string, filter nosql.Filter) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	expr, names, values, err := buildFilterExpression(filter)
	if err != nil {
		return 0, err
	}

	in := &ddb.ScanInput{TableName: s.table(table), Select: types.SelectCount}
	if expr != "" {
		in.FilterExpression = aws.String(expr)
		in.ExpressionAttributeNames = names
		in.ExpressionAttributeValues = values
	}

	var total int64
	for {
		out, err := s.client.Scan(ctx, in)
		if err != nil {
			return 0, fmt.Errorf("dynamodb: count in %s: %w", table, err)
		}
		total += int64(out.Count)
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
	return total, nil
}

// Ping verifies credentials and connectivity with a cheap control-plane call.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if _, err := s.client.ListTables(ctx, &ddb.ListTablesInput{Limit: aws.Int32(1)}); err != nil {
		return fmt.Errorf("dynamodb: ping region %s: %w", s.cfg.Region, err)
	}
	return nil
}

// Close is a no-op: the SDK client holds no long-lived connection of its own,
// but the method keeps Store interchangeable with the other NoSQL backends.
func (s *Store) Close(context.Context) error { return nil }
