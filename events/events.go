// Package events defines the event types emitted by gonveyor on node state transitions.
package events

import (
	"context"

	"github.com/terapps/gonveyor/ledger"
)

// Event is emitted on every node state transition.
type Event struct {
	Type ledger.EventType `json:"type"`
	Node ledger.Node      `json:"node"`
}

// Publisher receives events emitted on every node state transition.
// Implement this interface to forward events to a message broker, webhook, or any sink.
// If not set, events are silently dropped.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}
