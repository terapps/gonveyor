// Package amqp provides an AMQP-backed worker and dispatcher for gonveyor.
package amqp

import (
	"context"
	"os/signal"
	"sync"
	"syscall"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/terapps/gonveyor"
)

// ConnectionOption is a functional option for Connection.
type ConnectionOption func(*Connection)

// Connection wraps a single AMQP connection shared across Dispatcher and Worker.
type Connection struct {
	mu   sync.RWMutex
	conn *amqp091.Connection
	url  string
}

// Dial opens a single AMQP connection to the given URL.
func Dial(url string, opts ...ConnectionOption) (*Connection, error) {
	conn, err := amqp091.Dial(url)
	if err != nil {
		return nil, err
	}

	c := &Connection{conn: conn, url: url}
	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// NewDispatcher creates a Dispatcher that publishes to the given queue.
func (c *Connection) NewDispatcher(queue *Queue) (*Dispatcher, error) {
	ch, err := c.channel()
	if err != nil {
		return nil, err
	}

	d := &Dispatcher{conn: c, ch: ch, queue: queue, retryFn: defaultDispatcherRetry()}
	if err := d.declareQueue(ch); err != nil {
		_ = ch.Close()
		return nil, err
	}

	return d, nil
}

// NewWorker creates a Worker that consumes from the given queue.
func (c *Connection) NewWorker(queue *Queue, opts ...WorkerOption) (*Worker, error) {
	w := applyOptions(Worker{
		conn:      c,
		queue:     queue,
		config:    WorkerConfig{Prefetch: 1, Concurrency: 1},
		requeueFn: func(error) bool { return false },
		retryFn: retryBackoff(RetryConfig{
			InitialDelay: time.Second,
			Factor:       2,
			MaxRetries:   5,
		}),
		shutdownFn: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, syscall.SIGTERM)
		},
	}, opts...)

	if err := w.openChannel(); err != nil {
		return nil, err
	}

	if queue.DeadLetter == "" {
		gonveyor.Logger.Warn("no dead letter queue configured — failed messages will be dropped")
	}

	return &w, nil
}

// Close closes the underlying AMQP connection.
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.conn.Close()
}

func (c *Connection) channel() (*amqp091.Channel, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.conn.Channel()
}

func (c *Connection) reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.conn.IsClosed() {
		return nil
	}

	conn, err := amqp091.Dial(c.url)
	if err != nil {
		return err
	}

	_ = c.conn.Close()
	c.conn = conn

	return nil
}
