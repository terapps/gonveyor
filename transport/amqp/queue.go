package amqp

import (
	"errors"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

// ExchangeType defines the AMQP exchange routing strategy.
type ExchangeType string

const (
	// ExchangeDirect routes messages to queues whose binding key exactly matches the routing key.
	ExchangeDirect ExchangeType = amqp091.ExchangeDirect
	// ExchangeTopic routes messages to queues whose binding key matches a pattern.
	ExchangeTopic ExchangeType = amqp091.ExchangeTopic
)

// ErrMissingExchangeName is returned when a topic exchange is configured without a name.
var ErrMissingExchangeName = errors.New("exchange name required for topic exchange type")

// ErrMissingRoutingKey is returned when a topic exchange is configured without a routing key.
var ErrMissingRoutingKey = errors.New("routing key required for topic exchange type")

// Queue holds the AMQP queue configuration.
type Queue struct {
	Name         string
	Exchange     string
	ExchangeType ExchangeType
	RoutingKey   string
	Durable      bool
	AutoDelete   bool
	DeadLetter   string
}

// QueueOption is a functional option for Queue.
type QueueOption = func(*Queue)

// WithExchange configures the queue to use a named exchange of the given type.
func WithExchange(name string, t ExchangeType) QueueOption {
	return func(q *Queue) {
		q.Exchange = name
		q.ExchangeType = t
	}
}

// WithRoutingKey sets the routing key used when binding to a topic exchange.
func WithRoutingKey(key string) QueueOption {
	return func(q *Queue) { q.RoutingKey = key }
}

// WithDeadLetter configures a dead-letter exchange for undeliverable messages.
func WithDeadLetter(exchange string) QueueOption {
	return func(q *Queue) { q.DeadLetter = exchange }
}

// NewQueue creates a Queue with the given name and options.
func NewQueue(name string, opts ...QueueOption) (*Queue, error) {
	q := applyOptions(Queue{
		Name:         name,
		ExchangeType: ExchangeDirect,
		Durable:      true,
	}, opts...)

	if q.ExchangeType == ExchangeTopic && q.Exchange == "" {
		return nil, ErrMissingExchangeName
	}

	if q.ExchangeType == ExchangeTopic && q.RoutingKey == "" {
		return nil, ErrMissingRoutingKey
	}

	return &q, nil
}
