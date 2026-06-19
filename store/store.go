// Package store defines the persistence interfaces for gonveyor.
package store

import (
	"context"
	"encoding/json"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

// Task lifecycle statuses.
const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusDispatched TaskStatus = "dispatched"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusSuccess    TaskStatus = "success"
	TaskStatusFailed     TaskStatus = "failed"
)

// Blueprint is the domain representation of a workflow definition.
type Blueprint struct {
	ID   string
	Name string
}

// Task is the domain representation of a single unit of work within a blueprint.
type Task struct {
	ID          string
	BlueprintID string
	Key         string
	Status      TaskStatus
	Payload     []byte
	Result      []byte
	Error       string
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

// PendingTasks returns tasks that have no dependencies and can be dispatched immediately.
func (m BlueprintManifest) PendingTasks() []Task {
	blocked := make(map[string]struct{}, len(m.Dependencies))
	for _, d := range m.Dependencies {
		blocked[d.TaskID] = struct{}{}
	}

	pending := make([]Task, 0)

	for _, t := range m.Tasks {
		if _, ok := blocked[t.ID]; !ok {
			pending = append(pending, t)
		}
	}

	return pending
}

// Store is the unified persistence interface consumed by the orchestrator.
type Store interface {
	CreateBlueprint(ctx context.Context, manifest BlueprintManifest) error
	GetBlueprint(ctx context.Context, blueprintID string) (BlueprintManifest, error)

	GetTask(ctx context.Context, taskID string) (Task, error)
	SetDispatched(ctx context.Context, taskID string) (bool, error)
	SetRunning(ctx context.Context, taskID string) (bool, error)
	SetSuccess(ctx context.Context, taskID string, result any) (bool, error)
	SetFailed(ctx context.Context, taskID string, err error) error
	RenewLock(ctx context.Context, taskID string) error

	Pending(ctx context.Context, blueprintID string) ([]Task, error)
	Next(ctx context.Context, completedTaskID string) ([]Task, error)
	GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error)
}
