package gonveyor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/terapps/gonveyor/blueprint"
	"github.com/terapps/gonveyor/events"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

// TaskHandler is the user-facing handler type: business logic only, no transport concerns.
type TaskHandler func(ctx context.Context, task ledger.Unit) (any, error)

// Gonveyor orchestrates task execution across a distributed worker pool.
type Gonveyor struct {
	ledger         ledger.Ledger
	dispatcher     transport.Dispatcher
	worker         transport.Worker
	handlers       map[string]TaskHandler
	blueprints     map[string]*blueprint.Blueprint
	eventPublisher events.Publisher
}

// NewGonveyor creates a new Gonveyor with the given ledger, dispatcher and worker.
func NewGonveyor(l ledger.Ledger, dispatcher transport.Dispatcher, worker transport.Worker, opts ...Option) *Gonveyor {
	o := applyOptions(opts)
	return &Gonveyor{
		ledger:         l,
		dispatcher:     dispatcher,
		worker:         worker,
		handlers:       make(map[string]TaskHandler),
		blueprints:     make(map[string]*blueprint.Blueprint),
		eventPublisher: o.eventPublisher,
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

// OnComplete marks a unit as successful and publishes any newly unblocked units.
// Payload computation is deferred to the worker at Claim time.
func (o *Gonveyor) OnComplete(ctx context.Context, taskID string, result any) error {
	ok, tasks, err := o.ledger.RecordCompleted(ctx, taskID, result)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	for _, t := range tasks {
		if err := o.dispatcher.Publish(ctx, t); err != nil {
			return err
		}
	}

	return nil
}

func (o *Gonveyor) handler() transport.HandlerFunc {
	return func(ctx context.Context, task ledger.Unit, ack func()) (any, error) {
		fn, ok := o.handlers[task.Key]
		if !ok {
			return nil, fmt.Errorf("no handler registered for task key %q", task.Key)
		}

		// Build final input payload before claiming so it is stored in unit_started.output.
		// task.Payload carries the seed from AMQP; dep outputs are fetched here.
		payload := task.Payload
		if bp, ok := o.blueprints[task.BlueprintName]; ok {
			if node := bp.Node(task.Key); node != nil {
				var outputs map[string][]json.RawMessage
				var err error
				if node.NeedsDepData() {
					outputs, err = o.ledger.GatherDepResults(ctx, task.ID)
					if err != nil {
						return nil, err
					}
				}
				payload, err = node.BuildInput(task.Payload, outputs)
				if err != nil {
					return nil, err
				}
			}
		}

		keepalive, ok, err := o.ledger.Claim(ctx, task.ID, payload)
		if err != nil {
			return nil, err
		}
		if !ok {
			ack()
			return nil, nil
		}

		task.Payload = payload

		// Task claimed — ACK before exec so a crash during handler is recovered
		ack()

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		go o.heartbeat(ctx, task.ID, keepalive)

		result, err := fn(ctx, task)
		if err != nil {
			if ferr := o.ledger.RecordFailed(ctx, task.ID, err); ferr != nil {
				Logger.Error("RecordFailed failed", "task", task.ID, "err", ferr)
			}
			o.publish(ctx, events.Event{Type: events.EventUnitFailed, Unit: task})
			return nil, err
		}

		if err := o.OnComplete(ctx, task.ID, result); err != nil {
			Logger.Error("OnComplete failed", "task", task.ID, "err", err)
		}
		o.publish(ctx, events.Event{Type: events.EventUnitCompleted, Unit: task})

		return result, nil
	}
}

func (o *Gonveyor) publish(ctx context.Context, event events.Event) {
	if o.eventPublisher == nil {
		return
	}
	if err := o.eventPublisher.Publish(ctx, event); err != nil {
		Logger.Error("event publish failed", "type", event.Type, "unit", event.Unit.ID, "err", err)
	}
}

func (o *Gonveyor) heartbeat(ctx context.Context, taskID string, keepalive func() error) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := keepalive(); err != nil {
				Logger.Error("heartbeat failed", "task", taskID, "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
