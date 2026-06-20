package gonveyor

import (
	"context"

	"github.com/terapps/gonveyor/ledger"
)

// Event is emitted by Gonveyor and Gonductor on every node state transition.
type Event struct {
	Type ledger.EventType `json:"type"`
	Node ledger.Node      `json:"node"`
}

// EventPublisher receives events emitted on every node state transition.
// Implement this interface to forward events to a message broker, webhook, or any sink.
// If not set, events are silently dropped.
type EventPublisher interface {
	Publish(ctx context.Context, event Event) error
}
