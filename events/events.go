// Package events defines the event types emitted by gonveyor on unit state transitions.
package events

import (
	"context"

	"github.com/terapps/gonveyor/ledger"
)

type EventType string

const (
	EventUnitSeeded     EventType = "unit_seeded"
	EventUnitDispatched EventType = "unit_dispatched"
	EventUnitStarted    EventType = "unit_started"
	EventUnitCompleted  EventType = "unit_completed"
	EventUnitFailed     EventType = "unit_failed"
	EventUnitRetried    EventType = "unit_retried"
)

// Event is emitted on every unit state transition.
type Event struct {
	Type EventType   `json:"type"`
	Unit ledger.Unit `json:"unit"`
}

// Publisher receives events emitted on every unit state transition.
// Implement this interface to forward events to a message broker, webhook, or any sink.
// If not set, events are silently dropped.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}
