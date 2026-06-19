// Package gonveyor provides a task orchestration framework with typed dependency resolution.
package gonveyor

import (
	"context"

	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/transport"
)

// Gonductor submits blueprints and dispatches their initial tasks.
type Gonductor struct {
	store      store.Store
	dispatcher transport.Dispatcher
}

// NewGonductor creates a Gonductor backed by the given store and dispatcher.
func NewGonductor(store store.Store, dispatcher transport.Dispatcher) *Gonductor {
	return &Gonductor{store: store, dispatcher: dispatcher}
}

// Submit persists a blueprint manifest in the store.
func (c *Gonductor) Submit(ctx context.Context, manifest store.BlueprintManifest) error {
	return c.store.CreateBlueprint(ctx, manifest)
}

// Dispatch marks tasks as dispatched and publishes them to the queue.
func (c *Gonductor) Dispatch(ctx context.Context, tasks []store.Task) error {
	for _, t := range tasks {
		dispatched, err := c.store.SetDispatched(ctx, t.ID)
		if err != nil {
			return err
		}

		if !dispatched {
			continue
		}

		if err := c.dispatcher.Publish(ctx, t); err != nil {
			return err
		}
	}

	return nil
}
