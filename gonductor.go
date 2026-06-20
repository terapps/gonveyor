// Package gonveyor provides a task orchestration framework with typed dependency resolution.
package gonveyor

import (
	"context"

	"github.com/terapps/gonveyor/events"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

// Gonductor submits blueprints and dispatches their initial tasks.
type Gonductor struct {
	ledger         ledger.Ledger
	dispatcher     transport.Dispatcher
	eventPublisher events.Publisher
}

// NewGonductor creates a Gonductor backed by the given ledger and dispatcher.
func NewGonductor(l ledger.Ledger, dispatcher transport.Dispatcher, opts ...Option) *Gonductor {
	o := applyOptions(opts)
	return &Gonductor{
		ledger:         l,
		dispatcher:     dispatcher,
		eventPublisher: o.eventPublisher,
	}
}

// Launch persists a blueprint manifest and publishes its root task nodes atomically.
// Signal nodes are not published — activate them via SendSignal.
func (c *Gonductor) Launch(ctx context.Context, manifest ledger.BlueprintManifest) error {
	nodes, err := c.ledger.CreateBlueprint(ctx, manifest)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if err := c.dispatcher.Publish(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// SendSignal completes the signal node identified by signalKey within blueprintID
// and publishes any newly unblocked successors. Call this when an external event arrives
// (e.g. human approval, webhook callback).
func (c *Gonductor) SendSignal(ctx context.Context, blueprintID, signalKey string, payload any) error {
	nodes, err := c.ledger.SendSignal(ctx, blueprintID, signalKey, payload)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if err := c.dispatcher.Publish(ctx, n); err != nil {
			return err
		}
	}
	return nil
}
