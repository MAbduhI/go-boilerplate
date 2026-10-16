package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// TypedHandler is a generic handler function for unmarshalled Kafka payloads.
type TypedHandler[T any] func(ctx context.Context, msg Message, payload T) error

// RunTypedConsumer runs a robust consumer loop unmarshalling JSON into type T.
func RunTypedConsumer[T any](ctx context.Context, consumer *Consumer, handler TypedHandler[T]) error {
	log := logger.Module("kafka.typed_consumer")
	return consumer.Consume(ctx, func(ctx context.Context, msg Message) error {
		var payload T
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			log.Error("kafka unmarshal error: skipping corrupt message",
				slog.String("topic", msg.Topic),
				slog.String("key", string(msg.Key)),
				slog.String("error", err.Error()),
			)
			// Return nil so corrupt JSON is not redelivered indefinitely
			return nil
		}

		if err := handler(ctx, msg, payload); err != nil {
			log.Error("kafka handler failed",
				slog.String("topic", msg.Topic),
				slog.String("key", string(msg.Key)),
				slog.String("error", err.Error()),
			)
			return err
		}
		return nil
	})
}

// PublishJSON serializes payload as JSON and publishes to Kafka.
func PublishJSON(ctx context.Context, producer *Producer, topic string, key string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("kafka: failed to marshal json payload: %w", err)
	}
	return producer.Publish(ctx, Message{
		Topic: topic,
		Key:   []byte(key),
		Value: b,
		Time:  time.Now(),
	})
}
