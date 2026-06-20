package gonveyor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/transport"
)

// Gonveyor orchestrates task execution across a distributed worker pool.
type Gonveyor struct {
	store      store.Store
	dispatcher transport.Dispatcher
	worker     transport.Worker
	handlers   map[string]transport.HandlerFunc
	blueprints map[string]*blueprint.Blueprint
}

// NewGonveyor creates a new Gonveyor with the given store, dispatcher and worker.
func NewGonveyor(store store.Store, dispatcher transport.Dispatcher, worker transport.Worker) *Gonveyor {
	return &Gonveyor{
		store:      store,
		dispatcher: dispatcher,
		worker:     worker,
		handlers:   make(map[string]transport.HandlerFunc),
		blueprints: make(map[string]*blueprint.Blueprint),
	}
}

// RegisterBlueprint registers the wiring for a blueprint so the orchestrator
// knows how to build each task's input from upstream outputs.
func (o *Gonveyor) RegisterBlueprint(bp *blueprint.Blueprint) {
	o.blueprints[bp.Name()] = bp
}

// RegisterHandler registers a handler for a task key.
func (o *Gonveyor) RegisterHandler(def blueprint.AnyDef, fn transport.HandlerFunc) {
	o.handlers[def.Key()] = fn
}

// Listen starts consuming tasks and dispatches them to registered handlers.
func (o *Gonveyor) Listen(ctx context.Context) error {
	if err := o.worker.Listen(ctx, o.handler()); !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// OnComplete marks a task as successful and dispatches any newly unblocked tasks.
func (o *Gonveyor) OnComplete(ctx context.Context, taskID string, result any) error {
	ok, err := o.store.SetSuccess(ctx, taskID, result)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	tasks, err := o.store.Next(ctx, taskID)
	if err != nil {
		return err
	}

	for _, t := range tasks {
		dispatched, err := o.store.SetDispatched(ctx, t.ID)
		if err != nil {
			return err
		}

		if !dispatched {
			continue
		}

		if bp, ok := o.blueprints[t.BlueprintName]; ok {
			if node := bp.Node(t.Key); node != nil {
				var outputs map[string][]json.RawMessage
				if node.NeedsDepData() {
					outputs, err = o.store.GatherDepResults(ctx, t.ID)
					if err != nil {
						return err
					}
				}

				t.Payload, err = node.BuildInput(t.Payload, outputs)
				if err != nil {
					return err
				}
			}
		}

		if err := o.dispatcher.Publish(ctx, t); err != nil {
			return err
		}
	}

	return nil
}

func (o *Gonveyor) handler() transport.HandlerFunc {
	return func(ctx context.Context, task store.Task) (any, error) {
		fn, ok := o.handlers[task.Key]
		if !ok {
			return nil, fmt.Errorf("no handler registered for task key %q", task.Key)
		}

		ok, err := o.store.SetRunning(ctx, task.ID)
		if err != nil {
			return nil, err
		}

		if !ok {
			return nil, nil
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		go o.heartbeat(ctx, task.ID)

		result, err := fn(ctx, task)
		if err != nil {
			if ferr := o.store.SetFailed(ctx, task.ID, err); ferr != nil {
				Logger.Error("SetFailed failed", "task", task.ID, "err", ferr)
			}
			return nil, err
		}

		if err := o.OnComplete(ctx, task.ID, result); err != nil {
			return nil, err
		}

		return result, nil
	}
}

func (o *Gonveyor) heartbeat(ctx context.Context, taskID string) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = o.store.RenewLock(ctx, taskID)
		case <-ctx.Done():
			return
		}
	}
}
