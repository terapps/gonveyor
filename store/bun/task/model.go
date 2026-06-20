package task

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/ledger"
	"github.com/uptrace/bun"
)

// Task is the bun model for blueprint_tasks.
type Task struct {
	bun.BaseModel `bun:"table:blueprint_tasks"`

	ID          string          `bun:"id,pk,type:uuid"`
	BlueprintID string          `bun:"blueprint_id,notnull"`
	Key         string          `bun:"key,notnull"`
	PendingDeps int             `bun:"pending_deps,notnull"`
	Payload     json.RawMessage `bun:"payload,type:jsonb"`
	CreatedAt   time.Time       `bun:"created_at,notnull,default:now()"`
	UpdatedAt   time.Time       `bun:"updated_at,notnull,default:now()"`
}

// taskRow holds a Task joined with its blueprint name.
type taskRow struct {
	Task
	BlueprintName string `bun:"blueprint_name"`
}

func (r taskRow) toLedger() ledger.Task {
	return ledger.Task{
		ID:            r.ID,
		BlueprintID:   r.BlueprintID,
		BlueprintName: r.BlueprintName,
		Key:           r.Key,
		Payload:       r.Payload,
	}
}

// Dependency is the bun model for blueprint_task_dependencies.
type Dependency struct {
	bun.BaseModel `bun:"table:blueprint_task_dependencies"`

	TaskID      string `bun:"task_id,pk"`
	DependsOnID string `bun:"depends_on_id,pk"`
}

// EventType represents the type of a task lifecycle event.
type EventType string

const (
	EventTaskDispatched EventType = "task_dispatched"
	EventTaskStarted    EventType = "task_started"
	EventTaskCompleted  EventType = "task_completed"
	EventTaskFailed     EventType = "task_failed"
	EventTaskRetried    EventType = "task_retried"
)

// TaskEvent is the bun model for task_events (append-only).
// Output is populated for task_completed; Error for task_failed.
type TaskEvent struct {
	bun.BaseModel `bun:"table:task_events"`

	ID          int64           `bun:"id,pk,autoincrement"`
	TaskID      string          `bun:"task_id,notnull,type:uuid"`
	BlueprintID string          `bun:"blueprint_id,notnull,type:uuid"`
	Key         string          `bun:"key,notnull"`
	Type        EventType       `bun:"type,notnull"`
	Output      json.RawMessage `bun:"output,type:jsonb"`
	Error       string          `bun:"error"`
	EmittedAt   time.Time       `bun:"emitted_at,notnull,default:now()"`
}

// TaskHeartbeat is the bun model for task_heartbeats (liveness, purgeable).
type TaskHeartbeat struct {
	bun.BaseModel `bun:"table:task_heartbeats"`

	TaskID    string    `bun:"task_id,notnull,type:uuid"`
	EmittedAt time.Time `bun:"emitted_at,notnull,default:now()"`
}
