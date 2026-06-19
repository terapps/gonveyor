// Package transport defines the message transport interfaces for gonveyor.
package transport

import (
	"context"

	"github.com/terapps/gonveyor/store"
)

// HandlerFunc processes a task and returns its result.
type HandlerFunc func(ctx context.Context, task store.Task) (any, error)

// Dispatcher publishes tasks to the message queue.
type Dispatcher interface {
	Publish(ctx context.Context, task store.Task) error
	Close() error
}

// Worker consumes tasks from the message queue.
type Worker interface {
	Listen(ctx context.Context, handler HandlerFunc) error
	Close() error
}
