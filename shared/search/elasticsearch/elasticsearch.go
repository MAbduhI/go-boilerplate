// Package elasticsearch wraps the official Elasticsearch v8 client.
//
// The client is fetched rather than built: it handles node discovery, retries,
// the bulk API framing and product-version checks, all of which are easy to get
// subtly wrong by hand.
package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	es "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// ErrNotFound reports a document that does not exist.
var ErrNotFound = errors.New("elasticsearch: document not found")

// Config describes the cluster connection.
type Config struct {
	Addresses []string

	Username string
	Password string
	APIKey   string

	// CloudID and its API key are the Elastic Cloud alternative to Addresses.
	CloudID string

	MaxRetries    int
	RetryOnStatus []int
	Timeout       time.Duration

	// DefaultIndex is used when a call passes an empty index name.
	DefaultIndex string
}

// LoadConfig reads Config from ELASTICSEARCH_* environment variables.
func LoadConfig() Config {
	return Config{
		Addresses: config.Strings("ELASTICSEARCH_ADDRESSES", []string{"http://localhost:9200"}),

		Username: config.String("ELASTICSEARCH_USERNAME", ""),
		Password: config.String("ELASTICSEARCH_PASSWORD", ""),
		APIKey:   config.String("ELASTICSEARCH_API_KEY", ""),
		CloudID:  config.String("ELASTICSEARCH_CLOUD_ID", ""),

		MaxRetries:    config.Int("ELASTICSEARCH_MAX_RETRIES", 3),
		RetryOnStatus: []int{502, 503, 504, 429},
		Timeout:       config.Duration("ELASTICSEARCH_TIMEOUT", 10*time.Second),

		DefaultIndex: config.String("ELASTICSEARCH_DEFAULT_INDEX", ""),
	}
}

// Client is the Elasticsearch wrapper.
type Client struct {
	es  *es.Client
	cfg Config
	log *slog.Logger
}

// New builds the client and verifies the cluster is reachable.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	esCfg := es.Config{
		Addresses:     cfg.Addresses,
		Username:      cfg.Username,
		Password:      cfg.Password,
		APIKey:        cfg.APIKey,
		CloudID:       cfg.CloudID,
		MaxRetries:    cfg.MaxRetries,
		RetryOnStatus: cfg.RetryOnStatus,
		Transport: &http.Transport{
			ResponseHeaderTimeout: cfg.Timeout,
			MaxIdleConnsPerHost:   10,
		},
	}

	raw, err := es.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: new client: %w", err)
	}

	c := &Client{es: raw, cfg: cfg, log: logger.Module("elasticsearch")}

	if err := c.Ping(ctx); err != nil {
		return nil, err
	}

	c.log.Info("connected",
		slog.String("addresses", strings.Join(cfg.Addresses, ",")),
		slog.String("default_index", cfg.DefaultIndex))

	return c, nil
}

// ES exposes the underlying client for APIs this wrapper does not cover.
func (c *Client) ES() *es.Client { return c.es }

// index resolves the index name, falling back to DefaultIndex.
func (c *Client) index(name string) (string, error) {
	if name != "" {
		return name, nil
	}
	if c.cfg.DefaultIndex == "" {
		return "", errors.New("elasticsearch: no index given and ELASTICSEARCH_DEFAULT_INDEX is unset")
	}
	return c.cfg.DefaultIndex, nil
}

// Index stores doc under id, creating or replacing it. An empty id lets
// Elasticsearch generate one.
func (c *Client) Index(ctx context.Context, index, id string, doc any) error {
	idx, err := c.index(index)
	if err != nil {
		return err
	}

	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("elasticsearch: marshal document: %w", err)
	}

	req := esapi.IndexRequest{
		Index:      idx,
		DocumentID: id,
		Body:       bytes.NewReader(body),
		Refresh:    "false",
	}

	res, err := req.Do(ctx, c.es)
	if err != nil {
		return fmt.Errorf("elasticsearch: index into %s: %w", idx, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch: index into %s: %s", idx, readError(res))
	}
	return nil
}

// Get fetches a document by id into dest.
func (c *Client) Get(ctx context.Context, index, id string, dest any) error {
	idx, err := c.index(index)
	if err != nil {
		return err
	}

	res, err := esapi.GetRequest{Index: idx, DocumentID: id}.Do(ctx, c.es)
	if err != nil {
		return fmt.Errorf("elasticsearch: get %s/%s: %w", idx, id, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.IsError() {
		return fmt.Errorf("elasticsearch: get %s/%s: %s", idx, id, readError(res))
	}

	// The document sits under _source; decoding the envelope and then the source
	// keeps callers from having to know the wire shape.
	var envelope struct {
		Source json.RawMessage `json:"_source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("elasticsearch: decode get response: %w", err)
	}
	if err := json.Unmarshal(envelope.Source, dest); err != nil {
		return fmt.Errorf("elasticsearch: unmarshal _source: %w", err)
	}
	return nil
}

// SearchResult is one page of hits.
type SearchResult struct {
	Total    int64
	MaxScore float64
	Hits     []Hit
}

// Hit is one matching document.
type Hit struct {
	ID     string
	Score  float64
	Source json.RawMessage
}

// Decode unmarshals a hit's source into dest.
func (h Hit) Decode(dest any) error {
	if err := json.Unmarshal(h.Source, dest); err != nil {
		return fmt.Errorf("elasticsearch: unmarshal hit %s: %w", h.ID, err)
	}
	return nil
}

// Search runs a query DSL body and returns the hits.
//
//	res, err := c.Search(ctx, "products", map[string]any{
//	    "query": map[string]any{"match": map[string]any{"name": "widget"}},
//	    "size":  20,
//	})
func (c *Client) Search(ctx context.Context, index string, query map[string]any) (*SearchResult, error) {
	idx, err := c.index(index)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: marshal query: %w", err)
	}

	res, err := c.es.Search(
		c.es.Search.WithContext(ctx),
		c.es.Search.WithIndex(idx),
		c.es.Search.WithBody(bytes.NewReader(body)),
		c.es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: search %s: %w", idx, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: search %s: %s", idx, readError(res))
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			MaxScore float64 `json:"max_score"`
			Hits     []struct {
				ID     string          `json:"_id"`
				Score  float64         `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode search response: %w", err)
	}

	out := &SearchResult{
		Total:    parsed.Hits.Total.Value,
		MaxScore: parsed.Hits.MaxScore,
		Hits:     make([]Hit, 0, len(parsed.Hits.Hits)),
	}
	for _, h := range parsed.Hits.Hits {
		out.Hits = append(out.Hits, Hit{ID: h.ID, Score: h.Score, Source: h.Source})
	}
	return out, nil
}

// Delete removes a document by id.
func (c *Client) Delete(ctx context.Context, index, id string) error {
	idx, err := c.index(index)
	if err != nil {
		return err
	}

	res, err := esapi.DeleteRequest{Index: idx, DocumentID: id}.Do(ctx, c.es)
	if err != nil {
		return fmt.Errorf("elasticsearch: delete %s/%s: %w", idx, id, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.IsError() {
		return fmt.Errorf("elasticsearch: delete %s/%s: %s", idx, id, readError(res))
	}
	return nil
}

// BulkItem is one operation in a bulk request.
type BulkItem struct {
	// Action is "index", "create", "update" or "delete".
	Action string
	Index  string
	ID     string
	Doc    any
}

// Bulk applies many operations in one request.
//
// Bulk always returns 200 even when individual items fail, so the per-item
// errors are inspected here; treating the HTTP status as success is the classic
// way to silently lose half a batch.
func (c *Client) Bulk(ctx context.Context, items []BulkItem) error {
	if len(items) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, item := range items {
		idx, err := c.index(item.Index)
		if err != nil {
			return err
		}

		meta := map[string]any{"_index": idx}
		if item.ID != "" {
			meta["_id"] = item.ID
		}
		action := item.Action
		if action == "" {
			action = "index"
		}

		if err := json.NewEncoder(&buf).Encode(map[string]any{action: meta}); err != nil {
			return fmt.Errorf("elasticsearch: encode bulk action: %w", err)
		}
		if action != "delete" {
			payload := item.Doc
			if action == "update" {
				payload = map[string]any{"doc": item.Doc}
			}
			if err := json.NewEncoder(&buf).Encode(payload); err != nil {
				return fmt.Errorf("elasticsearch: encode bulk document: %w", err)
			}
		}
	}

	res, err := c.es.Bulk(bytes.NewReader(buf.Bytes()), c.es.Bulk.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("elasticsearch: bulk: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch: bulk: %s", readError(res))
	}

	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("elasticsearch: decode bulk response: %w", err)
	}
	if !parsed.Errors {
		return nil
	}

	failed := make([]string, 0, 4)
	for _, item := range parsed.Items {
		for action, detail := range item {
			if len(detail.Error) > 0 {
				failed = append(failed, fmt.Sprintf("%s: %s", action, detail.Error))
			}
		}
	}
	return fmt.Errorf("elasticsearch: bulk had %d failed item(s): %s",
		len(failed), strings.Join(failed, "; "))
}

// Ping verifies the cluster is reachable.
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.es.Ping(c.es.Ping.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("elasticsearch: ping %s: %w", strings.Join(c.cfg.Addresses, ","), err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch: ping: %s", readError(res))
	}
	return nil
}

// Close releases idle connections.
//
// The client holds no session, only an HTTP transport, so this closes idle
// keep-alive connections. It exists so the connector fits the shutdown registry
// like every other one.
func (c *Client) Close() error {
	if transport, ok := c.es.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

// readError extracts a readable message from an error response.
func readError(res *esapi.Response) string {
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if err != nil || len(body) == 0 {
		return res.Status()
	}
	return fmt.Sprintf("%s: %s", res.Status(), strings.TrimSpace(string(body)))
}
