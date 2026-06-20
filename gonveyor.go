package gonveyor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

// TaskHandler is the user-facing handler type: business logic only, no transport concerns.
type TaskHandler func(ctx context.Context, task ledger.Task) (any, error)

// Gonveyor orchestrates task execution across a distributed worker pool.
type Gonveyor struct {
	ledger     ledger.Ledger
	dispatcher transport.Dispatcher
	worker     transport.Worker
	handlers   map[string]TaskHandler
	blueprints map[string]*blueprint.Blueprint
}

// NewGonveyor creates a new Gonveyor with the given ledger, dispatcher and worker.
func NewGonveyor(l ledger.Ledger, dispatcher transport.Dispatcher, worker transport.Worker) *Gonveyor {
	return &Gonveyor{
		ledger:     l,
		dispatcher: dispatcher,
		worker:     worker,
		handlers:   make(map[string]TaskHandler),
		blueprints: make(map[string]*blueprint.Blueprint),
	}
}

// RegisterBlueprint registers the wiring for a blueprint so the orchestrator
// knows how to build each task's input from upstream outputs.
func (o *Gonveyor) RegisterBlueprint(bp *blueprint.Blueprint) {
	o.blueprints[bp.Name()] = bp
}

// RegisterHandler registers a handler for a task key.
func (o *Gonveyor) RegisterHandler(def blueprint.AnyDef, fn TaskHandler) {
	o.handlers[def.Key()] = fn
}

// Listen starts consuming tasks and dispatches them to registered handlers.
func (o *Gonveyor) Listen(ctx context.Context) error {
	if err := o.worker.Listen(ctx, o.handler()); !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// OnComplete marks a task as successful and publishes any newly unblocked tasks.
func (o *Gonveyor) OnComplete(ctx context.Context, taskID string, result any) error {
	ok, tasks, err := o.ledger.SetSuccess(ctx, taskID, result)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	for _, t := range tasks {
		if bp, ok := o.blueprints[t.BlueprintName]; ok {
			if node := bp.Node(t.Key); node != nil {
				var outputs map[string][]json.RawMessage
				if node.NeedsDepData() {
					outputs, err = o.ledger.GatherDepResults(ctx, t.ID)
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
	return func(ctx context.Context, task ledger.Task, ack func()) (any, error) {
		fn, ok := o.handlers[task.Key]
		if !ok {
			return nil, fmt.Errorf("no handler registered for task key %q", task.Key)
		}

		ok, err := o.ledger.SetRunning(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			ack()
			return nil, nil
		}

		// Task claimed — ACK before exec so a crash during handler is recovered
		ack()

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		go o.heartbeat(ctx, task.ID)

		result, err := fn(ctx, task)
		if err != nil {
			if ferr := o.ledger.SetFailed(ctx, task.ID, err); ferr != nil {
				Logger.Error("SetFailed failed", "task", task.ID, "err", ferr)
			}
			return nil, err
		}

		if err := o.OnComplete(ctx, task.ID, result); err != nil {
			Logger.Error("OnComplete failed", "task", task.ID, "err", err)
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
			_ = o.ledger.RenewLock(ctx, taskID)
		case <-ctx.Done():
			return
		}
	}
}
