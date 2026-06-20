// Package transport defines the message transport interfaces for gonveyor.
package transport

import (
	"context"

	"github.com/terapps/gonveyor/ledger"
)

// HandlerFunc processes a task and returns its result.
// ack must be called once the task has been claimed (after SetRunning) to ACK
// the broker message before handler execution begins.
type HandlerFunc func(ctx context.Context, task ledger.Task, ack func()) (any, error)

// Dispatcher publishes tasks to the message queue.
type Dispatcher interface {
	Publish(ctx context.Context, task ledger.Task) error
	Close() error
}

// Worker consumes tasks from the message queue.
type Worker interface {
	Listen(ctx context.Context, handler HandlerFunc) error
	Close() error
}
