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
	DispatchedAt *time.Time
}

// Node is a persisted runtime instance of a workflow node within a blueprint.
// It corresponds to a blueprint.Station definition at execution time.
// NodeType is either "task" (executed by a worker) or "signal" (activated by SendSignal).
type Node struct {
	ID            string
	BlueprintID   string
	BlueprintName string
	Key           string
	NodeType      string
	Payload       []byte
}

// NodeDependency records that a node must complete before another can start.
type NodeDependency struct {
	NodeID      string
	DependsOnID string
}

// BlueprintManifest groups a blueprint with its nodes and dependency edges.
type BlueprintManifest struct {
	Blueprint       Blueprint
	Nodes           []Node
	NodeDependencies []NodeDependency
}

// RootNodes returns nodes that have no incoming dependencies.
func (m BlueprintManifest) RootNodes() []Node {
	blocked := make(map[string]struct{}, len(m.NodeDependencies))
	for _, d := range m.NodeDependencies {
		blocked[d.NodeID] = struct{}{}
	}
	out := make([]Node, 0)
	for _, n := range m.Nodes {
		if _, ok := blocked[n.ID]; !ok {
			out = append(out, n)
		}
	}
	return out
}

// Ledger is the unified persistence interface consumed by the orchestrator.
// CreateBlueprint atomically persists the manifest and dispatches root task nodes,
// returning them for immediate publication to the message queue.
// RecordCompleted atomically records completion, decrements downstream pending_deps,
// dispatches newly unblocked nodes, and returns them for publication.
// SendSignal completes a signal node and dispatches its newly unblocked successors.
type Ledger interface {
	CreateBlueprint(ctx context.Context, manifest BlueprintManifest) ([]Node, error)

	GetNode(ctx context.Context, nodeID string) (Node, error)
	Claim(ctx context.Context, nodeID string) (keepalive func() error, ok bool, err error)
	RecordCompleted(ctx context.Context, nodeID string, result any) (bool, []Node, error)
	RecordFailed(ctx context.Context, nodeID string, err error) error
	SendSignal(ctx context.Context, blueprintID string, signalKey string, payload any) ([]Node, error)

	GatherDepResults(ctx context.Context, nodeID string) (map[string][]json.RawMessage, error)
}
