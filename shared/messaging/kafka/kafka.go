// Package kafka wraps segmentio/kafka-go with a thin Producer and Consumer.
//
// kafka-go is chosen over the librdkafka-based clients because it is pure Go:
// no CGO means the service still cross-compiles and still builds on a scratch
// image.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config is shared by the producer and the consumer.
type Config struct {
	Brokers []string

	// Producer settings.
	Topic        string
	BatchSize    int
	BatchTimeout time.Duration
	WriteTimeout time.Duration
	MaxAttempts  int
	// RequiredAcks: 0 none, 1 leader, -1 all in-sync replicas.
	RequiredAcks int
	Async        bool
	Compression  string

	// Consumer settings.
	GroupID     string
	MinBytes    int
	MaxBytes    int
	MaxWait     time.Duration
	StartOffset string // "first" or "last"
	ReadTimeout time.Duration

	DialTimeout time.Duration
}

// LoadConfig reads Config from KAFKA_* environment variables.
func LoadConfig() Config {
	return Config{
		Brokers: config.Strings("KAFKA_BROKERS", []string{"localhost:9092"}),

		Topic:        config.String("KAFKA_TOPIC", ""),
		BatchSize:    config.Int("KAFKA_BATCH_SIZE", 100),
		BatchTimeout: config.Duration("KAFKA_BATCH_TIMEOUT", 1*time.Second),
		WriteTimeout: config.Duration("KAFKA_WRITE_TIMEOUT", 10*time.Second),
		MaxAttempts:  config.Int("KAFKA_MAX_ATTEMPTS", 3),
		RequiredAcks: config.Int("KAFKA_REQUIRED_ACKS", -1),
		Async:        config.Bool("KAFKA_ASYNC", false),
		Compression:  config.String("KAFKA_COMPRESSION", ""),

		GroupID:     config.String("KAFKA_GROUP_ID", ""),
		MinBytes:    config.Int("KAFKA_MIN_BYTES", 1),
		MaxBytes:    config.Int("KAFKA_MAX_BYTES", 10<<20), // 10 MiB
		MaxWait:     config.Duration("KAFKA_MAX_WAIT", 500*time.Millisecond),
		StartOffset: config.String("KAFKA_START_OFFSET", "last"),
		ReadTimeout: config.Duration("KAFKA_READ_TIMEOUT", 10*time.Second),

		DialTimeout: config.Duration("KAFKA_DIAL_TIMEOUT", 10*time.Second),
	}
}

func compressionCodec(name string) kafkago.Compression {
	switch name {
	case "gzip":
		return kafkago.Gzip
	case "snappy":
		return kafkago.Snappy
	case "lz4":
		return kafkago.Lz4
	case "zstd":
		return kafkago.Zstd
	default:
		return 0
	}
}

// Message is one Kafka record.
type Message struct {
	Topic   string // overrides the producer's default topic
	Key     []byte
	Value   []byte
	Headers map[string]string
	Time    time.Time
}

func (m Message) toKafkaGo(defaultTopic string) kafkago.Message {
	topic := m.Topic
	if topic == "" {
		topic = defaultTopic
	}
	headers := make([]kafkago.Header, 0, len(m.Headers))
	for k, v := range m.Headers {
		headers = append(headers, kafkago.Header{Key: k, Value: []byte(v)})
	}
	return kafkago.Message{
		Topic:   topic,
		Key:     m.Key,
		Value:   m.Value,
		Headers: headers,
		Time:    m.Time,
	}
}

// Producer publishes records.
type Producer struct {
	writer *kafkago.Writer
	cfg    Config
	log    *slog.Logger
}

// NewProducer builds a producer. kafka-go dials lazily, so this does not fail on
// an unreachable broker; call Ping to check connectivity at boot.
func NewProducer(cfg Config) *Producer {
	writer := &kafkago.Writer{
		Addr:         kafkago.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafkago.Hash{},
		BatchSize:    cfg.BatchSize,
		BatchTimeout: cfg.BatchTimeout,
		WriteTimeout: cfg.WriteTimeout,
		MaxAttempts:  cfg.MaxAttempts,
		RequiredAcks: kafkago.RequiredAcks(cfg.RequiredAcks),
		Async:        cfg.Async,
		Compression:  compressionCodec(cfg.Compression),
	}

	return &Producer{writer: writer, cfg: cfg, log: logger.Module("kafka.producer")}
}

// Writer exposes the underlying writer.
func (p *Producer) Writer() *kafkago.Writer { return p.writer }

// Publish writes one record.
func (p *Producer) Publish(ctx context.Context, msg Message) error {
	return p.PublishBatch(ctx, []Message{msg})
}

// PublishBatch writes several records in one request.
func (p *Producer) PublishBatch(ctx context.Context, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}

	out := make([]kafkago.Message, 0, len(msgs))
	for _, m := range msgs {
		km := m.toKafkaGo(p.cfg.Topic)
		if km.Topic == "" {
			return errors.New("kafka: message has no topic and KAFKA_TOPIC is unset")
		}
		out = append(out, km)
	}

	if err := p.writer.WriteMessages(ctx, out...); err != nil {
		return fmt.Errorf("kafka: write %d message(s): %w", len(out), err)
	}
	return nil
}

// Ping checks a broker is reachable.
func (p *Producer) Ping(ctx context.Context) error {
	if len(p.cfg.Brokers) == 0 {
		return errors.New("kafka: no brokers configured")
	}
	dialer := &kafkago.Dialer{Timeout: p.cfg.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("kafka: dial %s: %w", p.cfg.Brokers[0], err)
	}
	return conn.Close()
}

// Close flushes buffered records and closes the writer.
//
// With Async enabled this is the only thing that guarantees pending records were
// actually sent, which is why it belongs in the shutdown registry.
func (p *Producer) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("kafka: close producer: %w", err)
	}
	return nil
}

// Handler processes one consumed record. Returning an error stops the consumer
// without committing that record's offset, so it is redelivered on restart.
type Handler func(ctx context.Context, msg Message) error

// Consumer reads records from one topic as part of a consumer group.
type Consumer struct {
	reader *kafkago.Reader
	cfg    Config
	log    *slog.Logger
}

// NewConsumer builds a consumer for topic.
func NewConsumer(cfg Config, topic string) *Consumer {
	if topic == "" {
		topic = cfg.Topic
	}

	startOffset := kafkago.LastOffset
	if cfg.StartOffset == "first" || cfg.StartOffset == "earliest" {
		startOffset = kafkago.FirstOffset
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     cfg.Brokers,
		Topic:       topic,
		GroupID:     cfg.GroupID,
		MinBytes:    cfg.MinBytes,
		MaxBytes:    cfg.MaxBytes,
		MaxWait:     cfg.MaxWait,
		StartOffset: startOffset,
		Dialer:      &kafkago.Dialer{Timeout: cfg.DialTimeout, DualStack: true},
	})

	return &Consumer{
		reader: reader,
		cfg:    cfg,
		log:    logger.Module("kafka.consumer").With(slog.String("topic", topic)),
	}
}

// Reader exposes the underlying reader.
func (c *Consumer) Reader() *kafkago.Reader { return c.reader }

// Consume blocks, dispatching records to handler until ctx is cancelled.
//
// Offsets are committed only after the handler succeeds, giving at-least-once
// delivery: a crash mid-handler redelivers the record rather than losing it, so
// handlers must be idempotent.
func (c *Consumer) Consume(ctx context.Context, handler Handler) error {
	if c.cfg.GroupID == "" {
		return errors.New("kafka: KAFKA_GROUP_ID must be set to consume")
	}

	for {
		km, err := c.reader.FetchMessage(ctx)
		if err != nil {
			// A cancelled context is an orderly shutdown, not a failure.
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return fmt.Errorf("kafka: fetch message: %w", err)
		}

		headers := make(map[string]string, len(km.Headers))
		for _, h := range km.Headers {
			headers[h.Key] = string(h.Value)
		}

		msg := Message{
			Topic:   km.Topic,
			Key:     km.Key,
			Value:   km.Value,
			Headers: headers,
			Time:    km.Time,
		}

		if err := handler(ctx, msg); err != nil {
			c.log.Error("handler failed; offset not committed",
				slog.Int64("offset", km.Offset),
				slog.Int("partition", km.Partition),
				slog.String("error", err.Error()))
			return fmt.Errorf("kafka: handle message at offset %d: %w", km.Offset, err)
		}

		if err := c.reader.CommitMessages(ctx, km); err != nil {
			return fmt.Errorf("kafka: commit offset %d: %w", km.Offset, err)
		}
	}
}

// Close stops the consumer and commits nothing further.
func (c *Consumer) Close() error {
	if err := c.reader.Close(); err != nil {
		return fmt.Errorf("kafka: close consumer: %w", err)
	}
	return nil
}
