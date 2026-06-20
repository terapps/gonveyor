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

// Launch persists a blueprint manifest and dispatches its initial tasks in one call.
func (c *Gonductor) Launch(ctx context.Context, manifest store.BlueprintManifest) error {
	if err := c.store.CreateBlueprint(ctx, manifest); err != nil {
		return err
	}
	return c.DispatchBlueprint(ctx, manifest.Blueprint.ID)
}

// DispatchBlueprint fetches all pending tasks for a blueprint and dispatches them.
func (c *Gonductor) DispatchBlueprint(ctx context.Context, blueprintID string) error {
	tasks, err := c.store.Pending(ctx, blueprintID)
	if err != nil {
		return err
	}
	return c.Dispatch(ctx, tasks)
}

// Dispatch marks tasks as dispatched and publishes them to the queue.
func (c *Gonductor) Dispatch(ctx context.Context, tasks []store.Task) error {
	dispatched := false
	for _, t := range tasks {
		ok, err := c.store.SetDispatched(ctx, t.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := c.dispatcher.Publish(ctx, t); err != nil {
			return err
		}
		dispatched = true
	}

	if dispatched && len(tasks) > 0 {
		_ = c.store.SetBlueprintDispatched(ctx, tasks[0].BlueprintID)
	}

	return nil
}
