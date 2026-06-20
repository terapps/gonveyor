// Package gonveyor provides a task orchestration framework with typed dependency resolution.
package gonveyor

import (
	"context"

	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

// Gonductor submits blueprints and dispatches their initial tasks.
type Gonductor struct {
	ledger     ledger.Ledger
	dispatcher transport.Dispatcher
}

// NewGonductor creates a Gonductor backed by the given ledger and dispatcher.
func NewGonductor(l ledger.Ledger, dispatcher transport.Dispatcher) *Gonductor {
	return &Gonductor{ledger: l, dispatcher: dispatcher}
}

// Launch persists a blueprint manifest and publishes its root tasks atomically.
func (c *Gonductor) Launch(ctx context.Context, manifest ledger.BlueprintManifest) error {
	tasks, err := c.ledger.CreateBlueprint(ctx, manifest)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if err := c.dispatcher.Publish(ctx, t); err != nil {
			return err
		}
	}
	return nil
}
