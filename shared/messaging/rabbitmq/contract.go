// Package rabbitmq declares the messaging contract only.
//
// There is deliberately no implementation here. The project already has its own
// RabbitMQ connector, and a second half-considered one — guessing at exchange
// topology, redelivery and prefetch — would be worse than none: it would look
// usable and behave subtly wrong under load. Bind the existing connector to
// these interfaces and the rest of the codebase depends on the contract rather
// than on a driver.
package rabbitmq

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented is returned by the no-op implementations below.
var ErrNotImplemented = errors.New("rabbitmq: no implementation wired; provide your own Publisher/Consumer")

// Message is one AMQP message, in both directions.
type Message struct {
	// Exchange and RoutingKey address the message when publishing.
	Exchange   string
	RoutingKey string

	Body        []byte
	ContentType string
	Headers     map[string]any

	// MessageID and CorrelationID support deduplication and request/reply.
	MessageID     string
	CorrelationID string

	// Priority and Expiration map onto the AMQP fields of the same name.
	Priority   uint8
	Expiration time.Duration

	// Persistent asks the broker to write the message to disk.
	Persistent bool

	// Queue, DeliveryTag and Redelivered are set on consumed messages only.
	Queue       string
	DeliveryTag uint64
	Redelivered bool
	ConsumerTag string
	ReceivedAt  time.Time
}

// Publisher sends messages to an exchange.
type Publisher interface {
	// Publish sends one message.
	Publish(ctx context.Context, msg Message) error

	// PublishBatch sends several messages, ideally in one round trip.
	PublishBatch(ctx context.Context, msgs []Message) error

	// Close releases the connection.
	Close() error
}

// Handler processes one consumed message.
//
// Returning nil acknowledges the message. Returning an error negatively
// acknowledges it; whether it is requeued is the implementation's policy, and
// that policy must be documented by whoever wires this up — silent infinite
// requeue is the classic way to melt a broker.
type Handler func(ctx context.Context, msg Message) error

// ConsumeOptions configures one subscription.
type ConsumeOptions struct {
	Queue       string
	ConsumerTag string

	// Prefetch caps unacknowledged messages per consumer. Zero means unlimited,
	// which is almost never what you want.
	Prefetch int

	// Concurrency is how many handler goroutines run.
	Concurrency int

	// AutoAck acknowledges on delivery rather than after the handler returns.
	// It trades at-least-once for at-most-once delivery.
	AutoAck bool

	// RequeueOnError asks for a failed message to go back to the queue.
	RequeueOnError bool
}

// Consumer subscribes to a queue.
type Consumer interface {
	// Consume blocks, dispatching deliveries to handler until ctx is cancelled
	// or the connection drops.
	Consume(ctx context.Context, opts ConsumeOptions, handler Handler) error

	// Close stops consuming and releases the connection.
	Close() error
}

// Broker is a Publisher and a Consumer sharing one connection.
type Broker interface {
	Publisher
	Consumer

	// Ping verifies the connection, for a health check.
	Ping(ctx context.Context) error
}

// NoopPublisher satisfies Publisher without doing anything, so the app can be
// wired and compiled before the real connector is attached.
type NoopPublisher struct{}

var _ Publisher = NoopPublisher{}

func (NoopPublisher) Publish(context.Context, Message) error        { return ErrNotImplemented }
func (NoopPublisher) PublishBatch(context.Context, []Message) error { return ErrNotImplemented }
func (NoopPublisher) Close() error                                  { return nil }

// NoopConsumer satisfies Consumer without doing anything.
type NoopConsumer struct{}

var _ Consumer = NoopConsumer{}

func (NoopConsumer) Consume(context.Context, ConsumeOptions, Handler) error {
	return ErrNotImplemented
}
func (NoopConsumer) Close() error { return nil }
