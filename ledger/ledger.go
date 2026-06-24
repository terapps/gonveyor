// Package ledger defines the persistence interfaces for gonveyor.
package ledger

import (
	"context"
	"encoding/json"
	"time"
)

// Blueprint is the domain representation of a workflow instance.
type Blueprint struct {
	ID           string
	Name         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Unit is a persisted runtime instance of a workflow node within a blueprint.
// It corresponds to a blueprint.Station definition at execution time.
// UnitType is either "task" (executed by a worker) or "signal" (activated by SendSignal).
type Unit struct {
	ID            string
	BlueprintID   string
	BlueprintName string
	Key           string
	UnitType      string
	Payload       []byte
}

// UnitDependency records that a unit must complete before another can start.
type UnitDependency struct {
	UnitID      string
	DependsOnID string
}

// BlueprintManifest groups a blueprint with its units and dependency edges.
type BlueprintManifest struct {
	Blueprint        Blueprint
	Units            []Unit
	UnitDependencies []UnitDependency
}

// RootUnits returns units that have no incoming dependencies.
func (m BlueprintManifest) RootUnits() []Unit {
	blocked := make(map[string]struct{}, len(m.UnitDependencies))
	for _, d := range m.UnitDependencies {
		blocked[d.UnitID] = struct{}{}
	}
	out := make([]Unit, 0)
	for _, n := range m.Units {
		if _, ok := blocked[n.ID]; !ok {
			out = append(out, n)
		}
	}
	return out
}

// Ledger is the unified persistence interface consumed by the orchestrator.
// CreateBlueprint atomically persists the manifest and dispatches root task units,
// returning them for immediate publication to the message queue.
// RecordCompleted atomically records completion, decrements downstream pending_deps,
// dispatches newly unblocked units, and returns them for publication.
// SendSignal completes a signal unit and dispatches its newly unblocked successors.
type Ledger interface {
	CreateBlueprint(ctx context.Context, manifest BlueprintManifest) ([]Unit, error)

	GetUnit(ctx context.Context, unitID string) (Unit, error)
	Claim(ctx context.Context, unitID string, payload json.RawMessage) (keepalive func() error, ok bool, err error)
	RecordCompleted(ctx context.Context, unitID string, result any) (bool, []Unit, error)
	RecordFailed(ctx context.Context, unitID string, err error) error
	SendSignal(ctx context.Context, blueprintID string, signalKey string, payload any) ([]Unit, error)

	GatherDepResults(ctx context.Context, unitID string) (map[string][]json.RawMessage, error)
}
