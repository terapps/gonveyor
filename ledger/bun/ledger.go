package bun

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/terapps/gonveyor/ledger"
	bunblueprint "github.com/terapps/gonveyor/ledger/bun/blueprint"
	"github.com/terapps/gonveyor/ledger/bun/bunutil"
	bunevent "github.com/terapps/gonveyor/ledger/bun/event"
	bunnode "github.com/terapps/gonveyor/ledger/bun/node"
	"github.com/uptrace/bun"
)

var _ ledger.Ledger = (*Ledger)(nil)

// errAlreadyCompleted is used inside doRecordCompleted to abort the transaction on
// idempotent redelivery. Swallowed by the caller, which returns (false, nil, nil).
var errAlreadyCompleted = errors.New("node already completed")

type Ledger struct {
	db            *bun.DB
	blueprintRepo *bunblueprint.Repository
	nodeRepo      *bunnode.Repository
	eventRepo     *bunevent.Repository
}

func New(db *bun.DB) *Ledger {
	return &Ledger{
		db:            db,
		blueprintRepo: bunblueprint.New(db),
		nodeRepo:      bunnode.New(db),
		eventRepo:     bunevent.New(db),
	}
}

// CreateBlueprint atomically persists the manifest, records node_dispatched
// events for root task nodes, and returns those nodes for publication.
// Signal nodes are never auto-dispatched — they wait for SendSignal.
func (l *Ledger) CreateBlueprint(ctx context.Context, manifest ledger.BlueprintManifest) ([]ledger.Node, error) {
	depCount := make(map[string]int, len(manifest.Nodes))
	for _, d := range manifest.NodeDependencies {
		depCount[d.NodeID]++
	}

	var rootNodes []ledger.Node

	err := bunutil.RunInTx(ctx, l.db, func(ctx context.Context) error {
		now := time.Now()
		bp := &bunblueprint.Blueprint{
			ID:           manifest.Blueprint.ID,
			Name:         manifest.Blueprint.Name,
			DispatchedAt: &now,
		}
		if err := l.blueprintRepo.Insert(ctx, bp); err != nil {
			return err
		}

		nodes := make([]*bunnode.Node, len(manifest.Nodes))
		for i, n := range manifest.Nodes {
			nodes[i] = &bunnode.Node{
				ID:          n.ID,
				BlueprintID: n.BlueprintID,
				Key:         n.Key,
				NodeType:    n.NodeType,
				PendingDeps: depCount[n.ID],
				Payload:     n.Payload,
			}
		}
		if err := l.nodeRepo.Insert(ctx, nodes); err != nil {
			return err
		}

		deps := make([]*bunnode.Dependency, len(manifest.NodeDependencies))
		for i, d := range manifest.NodeDependencies {
			deps[i] = &bunnode.Dependency{
				NodeID:      d.NodeID,
				DependsOnID: d.DependsOnID,
			}
		}
		if err := l.nodeRepo.InsertDependencies(ctx, deps); err != nil {
			return err
		}

		// Dispatch root task nodes (pending_deps == 0, node_type == "task") atomically.
		// Signal nodes are excluded — they are activated via SendSignal, not the queue.
		rootEvents := make([]*bunevent.NodeEvent, 0)
		for _, n := range manifest.Nodes {
			if depCount[n.ID] == 0 && n.NodeType != bunnode.NodeTypeSignal {
				rootEvents = append(rootEvents, &bunevent.NodeEvent{
					NodeID:      n.ID,
					BlueprintID: n.BlueprintID,
					Key:         n.Key,
					Type:        ledger.EventNodeDispatched,
				})
				rootNodes = append(rootNodes, n)
			}
		}
		return l.eventRepo.Insert(ctx, rootEvents)
	})
	if err != nil {
		return nil, err
	}

	return rootNodes, nil
}

func (l *Ledger) GetNode(ctx context.Context, nodeID string) (ledger.Node, error) {
	return l.nodeRepo.Get(ctx, nodeID)
}

func (l *Ledger) Claim(ctx context.Context, nodeID string) (func() error, bool, error) {
	ok, err := l.eventRepo.RecordStarted(ctx, nodeID)
	if err != nil || !ok {
		return nil, ok, err
	}
	keepalive := func() error { return l.eventRepo.RecordHeartbeat(ctx, nodeID) }
	return keepalive, true, nil
}

func (l *Ledger) RecordCompleted(ctx context.Context, nodeID string, result any) (bool, []ledger.Node, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, nil, err
	}
	return l.doRecordCompleted(ctx, nodeID, raw)
}

func (l *Ledger) RecordFailed(ctx context.Context, nodeID string, nodeErr error) error {
	return l.eventRepo.RecordFailed(ctx, nodeID, nodeErr.Error())
}

func (l *Ledger) GatherDepResults(ctx context.Context, nodeID string) (map[string][]json.RawMessage, error) {
	return l.eventRepo.GatherDepResults(ctx, nodeID)
}

func (l *Ledger) SendSignal(ctx context.Context, blueprintID, signalKey string, payload any) ([]ledger.Node, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	node, err := l.nodeRepo.FindSignalNode(ctx, blueprintID, signalKey)
	if err != nil {
		return nil, err
	}
	_, unblocked, err := l.doRecordCompleted(ctx, node.ID, raw)
	return unblocked, err
}

// doRecordCompleted atomically records node_completed, cascades pending_deps to successors,
// and dispatches newly unblocked task nodes. Returns (false, nil, nil) on idempotent redelivery.
func (l *Ledger) doRecordCompleted(ctx context.Context, nodeID string, raw json.RawMessage) (bool, []ledger.Node, error) {
	var unblocked []ledger.Node

	err := bunutil.RunInTx(ctx, l.db, func(ctx context.Context) error {
		ok, err := l.eventRepo.RecordCompleted(ctx, nodeID, raw)
		if err != nil {
			return err
		}
		if !ok {
			return errAlreadyCompleted
		}

		if err := l.nodeRepo.DecrementPendingDeps(ctx, nodeID); err != nil {
			return err
		}

		rows, err := l.nodeRepo.SelectUnblocked(ctx, nodeID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		events := make([]*bunevent.NodeEvent, len(rows))
		for i, n := range rows {
			events[i] = &bunevent.NodeEvent{
				NodeID:      n.ID,
				BlueprintID: n.BlueprintID,
				Key:         n.Key,
				Type:        ledger.EventNodeDispatched,
			}
		}
		if err := l.eventRepo.Insert(ctx, events); err != nil {
			return err
		}

		unblocked = rows
		return nil
	})

	if errors.Is(err, errAlreadyCompleted) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, unblocked, nil
}
