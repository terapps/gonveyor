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

// Task is the domain representation of a single unit of work within a blueprint.
type Task struct {
	ID            string
	BlueprintID   string
	BlueprintName string
	Key           string
	Payload       []byte
}

// TaskDependency records that a task must complete before another can start.
type TaskDependency struct {
	TaskID      string
	DependsOnID string
}

// BlueprintManifest groups a blueprint with its tasks and dependency edges.
type BlueprintManifest struct {
	Blueprint    Blueprint
	Tasks        []Task
	Dependencies []TaskDependency
}

// RootTasks returns tasks that have no incoming dependencies.
func (m BlueprintManifest) RootTasks() []Task {
	blocked := make(map[string]struct{}, len(m.Dependencies))
	for _, d := range m.Dependencies {
		blocked[d.TaskID] = struct{}{}
	}
	out := make([]Task, 0)
	for _, t := range m.Tasks {
		if _, ok := blocked[t.ID]; !ok {
			out = append(out, t)
		}
	}
	return out
}

// Ledger is the unified persistence interface consumed by the orchestrator.
// CreateBlueprint atomically persists the manifest and dispatches root tasks,
// returning them for immediate publication to the message queue.
// SetSuccess atomically records completion, decrements downstream pending_deps,
// dispatches newly unblocked tasks, and returns them for publication.
type Ledger interface {
	CreateBlueprint(ctx context.Context, manifest BlueprintManifest) ([]Task, error)
	GetBlueprint(ctx context.Context, blueprintID string) (BlueprintManifest, error)
	ListBlueprints(ctx context.Context) ([]Blueprint, error)

	GetTask(ctx context.Context, taskID string) (Task, error)
	SetDispatched(ctx context.Context, taskID string) (bool, error)
	SetRunning(ctx context.Context, taskID string) (bool, error)
	SetSuccess(ctx context.Context, taskID string, result any) (bool, []Task, error)
	SetFailed(ctx context.Context, taskID string, err error) error
	RenewLock(ctx context.Context, taskID string) error

	GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error)
}
