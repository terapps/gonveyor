package bun

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/terapps/gonveyor/events"
	"github.com/terapps/gonveyor/ledger"
	bunblueprint "github.com/terapps/gonveyor/ledger/bun/blueprint"
	"github.com/terapps/gonveyor/ledger/bun/bunutil"
	bunevent "github.com/terapps/gonveyor/ledger/bun/event"
	bununit "github.com/terapps/gonveyor/ledger/bun/unit"
	"github.com/uptrace/bun"
)

var _ ledger.Ledger = (*Ledger)(nil)

// errAlreadyCompleted is used inside doRecordCompleted to abort the transaction on
// idempotent redelivery. Swallowed by the caller, which returns (false, nil, nil).
var errAlreadyCompleted = errors.New("unit already completed")

type Ledger struct {
	db            *bun.DB
	blueprintRepo *bunblueprint.Repository
	unitRepo      *bununit.Repository
	eventRepo     *bunevent.Repository
}

func New(db *bun.DB) *Ledger {
	return &Ledger{
		db:            db,
		blueprintRepo: bunblueprint.New(db),
		unitRepo:      bununit.New(db),
		eventRepo:     bunevent.New(db),
	}
}

// CreateBlueprint atomically persists the manifest, records unit_dispatched
// events for root task units, and returns those units for publication.
// Signal units are never auto-dispatched — they wait for SendSignal.
func (l *Ledger) CreateBlueprint(ctx context.Context, manifest ledger.BlueprintManifest) ([]ledger.Unit, error) {
	depCount := make(map[string]int, len(manifest.Units))
	for _, d := range manifest.UnitDependencies {
		depCount[d.UnitID]++
	}

	var rootUnits []ledger.Unit

	err := bunutil.RunInTx(ctx, l.db, func(ctx context.Context) error {
		bp := &bunblueprint.Blueprint{
			ID:   manifest.Blueprint.ID,
			Name: manifest.Blueprint.Name,
		}
		if err := l.blueprintRepo.Insert(ctx, bp); err != nil {
			return err
		}

		units := make([]*bununit.Unit, len(manifest.Units))
		for i, n := range manifest.Units {
			units[i] = &bununit.Unit{
				ID:          n.ID,
				BlueprintID: n.BlueprintID,
				Key:         n.Key,
				UnitType:    n.UnitType,
				PendingDeps: depCount[n.ID],
			}
		}
		if err := l.unitRepo.Insert(ctx, units); err != nil {
			return err
		}

		deps := make([]*bununit.Dependency, len(manifest.UnitDependencies))
		for i, d := range manifest.UnitDependencies {
			deps[i] = &bununit.Dependency{
				UnitID:      d.UnitID,
				DependsOnID: d.DependsOnID,
			}
		}
		if err := l.unitRepo.InsertDependencies(ctx, deps); err != nil {
			return err
		}

		// Persist seed payloads for all units that have one.
		// These are loaded back via LEFT JOIN on unit_seeded when units are dispatched.
		seedEvents := make([]*bunevent.UnitEvent, 0, len(manifest.Units))
		for _, n := range manifest.Units {
			if len(n.Payload) > 0 {
				seedEvents = append(seedEvents, &bunevent.UnitEvent{
					UnitID:      n.ID,
					BlueprintID: n.BlueprintID,
					Key:         n.Key,
					Type:        events.EventUnitSeeded,
					Output:      n.Payload,
				})
			}
		}
		if err := l.eventRepo.Insert(ctx, seedEvents); err != nil {
			return err
		}

		// Dispatch root task units (pending_deps == 0, unit_type == "task") atomically.
		// Signal units are excluded — they are activated via SendSignal, not the queue.
		rootEvents := make([]*bunevent.UnitEvent, 0)
		for _, n := range manifest.Units {
			if depCount[n.ID] == 0 && n.UnitType != bununit.UnitTypeSignal {
				rootEvents = append(rootEvents, &bunevent.UnitEvent{
					UnitID:      n.ID,
					BlueprintID: n.BlueprintID,
					Key:         n.Key,
					Type:        events.EventUnitDispatched,
				})
				rootUnits = append(rootUnits, n)
			}
		}
		return l.eventRepo.Insert(ctx, rootEvents)
	})
	if err != nil {
		return nil, err
	}

	return rootUnits, nil
}

func (l *Ledger) GetUnit(ctx context.Context, unitID string) (ledger.Unit, error) {
	return l.unitRepo.Get(ctx, unitID)
}

func (l *Ledger) Claim(ctx context.Context, unitID string, payload json.RawMessage) (func() error, bool, error) {
	ok, err := l.eventRepo.RecordStarted(ctx, unitID, payload)
	if err != nil || !ok {
		return nil, ok, err
	}
	keepalive := func() error { return l.eventRepo.RecordHeartbeat(ctx, unitID) }
	return keepalive, true, nil
}

func (l *Ledger) RecordCompleted(ctx context.Context, unitID string, result any) (bool, []ledger.Unit, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, nil, err
	}
	return l.doRecordCompleted(ctx, unitID, raw)
}

func (l *Ledger) RecordFailed(ctx context.Context, unitID string, unitErr error) error {
	return l.eventRepo.RecordFailed(ctx, unitID, unitErr.Error())
}

func (l *Ledger) GatherDepResults(ctx context.Context, unitID string) (map[string][]json.RawMessage, error) {
	return l.eventRepo.GatherDepResults(ctx, unitID)
}

func (l *Ledger) SendSignal(ctx context.Context, blueprintID, signalKey string, payload any) ([]ledger.Unit, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	unit, err := l.unitRepo.FindSignalUnit(ctx, blueprintID, signalKey)
	if err != nil {
		return nil, err
	}
	_, unblocked, err := l.doRecordCompleted(ctx, unit.ID, raw)
	return unblocked, err
}

// doRecordCompleted atomically records unit_completed, cascades pending_deps to successors,
// and dispatches newly unblocked task units. Returns (false, nil, nil) on idempotent redelivery.
func (l *Ledger) doRecordCompleted(ctx context.Context, unitID string, raw json.RawMessage) (bool, []ledger.Unit, error) {
	var unblocked []ledger.Unit

	err := bunutil.RunInTx(ctx, l.db, func(ctx context.Context) error {
		ok, err := l.eventRepo.RecordCompleted(ctx, unitID, raw)
		if err != nil {
			return err
		}
		if !ok {
			return errAlreadyCompleted
		}

		if err := l.unitRepo.DecrementPendingDeps(ctx, unitID); err != nil {
			return err
		}

		rows, err := l.unitRepo.SelectUnblocked(ctx, unitID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		unitEvents := make([]*bunevent.UnitEvent, len(rows))
		for i, n := range rows {
			unitEvents[i] = &bunevent.UnitEvent{
				UnitID:      n.ID,
				BlueprintID: n.BlueprintID,
				Key:         n.Key,
				Type:        events.EventUnitDispatched,
			}
		}
		if err := l.eventRepo.Insert(ctx, unitEvents); err != nil {
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
