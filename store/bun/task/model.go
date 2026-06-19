package task

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/store"
	"github.com/uptrace/bun"
)

type Task struct {
	bun.BaseModel `bun:"table:blueprint_tasks"`

	ID        string    `bun:"id,pk,type:uuid"`
	CreatedAt time.Time `bun:"created_at,notnull,default:now()"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:now()"`

	BlueprintID string           `bun:"blueprint_id,notnull"`
	Key         string           `bun:"key,notnull"`
	Status      store.TaskStatus `bun:"status,notnull"`
	Error       []byte           `bun:"error,type:text"`
	Payload     json.RawMessage  `bun:"payload,type:jsonb"`
	Result      json.RawMessage  `bun:"result,type:jsonb"`
	LockedUntil *time.Time       `bun:"locked_until"`
}

type Dependency struct {
	bun.BaseModel `bun:"table:blueprint_task_dependencies"`

	TaskID      string `bun:"task_id,pk"`
	DependsOnID string `bun:"depends_on_id,pk"`
}

func (t Task) ToStore() store.Task {
	return store.Task{
		ID:          t.ID,
		BlueprintID: t.BlueprintID,
		Key:         t.Key,
		Status:      t.Status,
		Payload:     t.Payload,
		Result:      t.Result,
		Error:       string(t.Error),
	}
}
