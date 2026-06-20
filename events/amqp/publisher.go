// Package amqp provides an AMQP fanout publisher for gonveyor events.
package amqp

import (
	"context"
	"encoding/json"
	"sync"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/terapps/gonveyor/events"
)

// Publisher publishes gonveyor events to an AMQP fanout exchange.
type Publisher struct {
	mu       sync.RWMutex
	conn     *amqp091.Connection
	ch       *amqp091.Channel
	exchange string
}

// New creates a Publisher that broadcasts events to the given fanout exchange.
// The exchange is declared on creation.
func New(conn *amqp091.Connection, exchange string) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	if err := ch.ExchangeDeclare(exchange, "fanout", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		return nil, err
	}

	return &Publisher{conn: conn, ch: ch, exchange: exchange}, nil
}

// Publish serializes the event and broadcasts it to all bound queues.
func (p *Publisher) Publish(ctx context.Context, event events.Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	p.mu.RLock()
	err = p.ch.PublishWithContext(ctx, p.exchange, "", false, false, amqp091.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	p.mu.RUnlock()

	return err
}

// Close shuts down the channel.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ch.Close()
}
