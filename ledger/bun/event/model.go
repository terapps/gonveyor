package event

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/events"
	"github.com/uptrace/bun"
)

// UnitEvent is the bun model for unit_events (append-only).
// Output is populated for unit_completed; Error for unit_failed.
type UnitEvent struct {
	bun.BaseModel `bun:"table:unit_events"`

	ID          int64            `bun:"id,pk,autoincrement"`
	UnitID      string           `bun:"unit_id,notnull,type:uuid"`
	BlueprintID string           `bun:"blueprint_id,notnull,type:uuid"`
	Key         string           `bun:"key,notnull"`
	Type        events.EventType `bun:"type,notnull"`
	Output      json.RawMessage  `bun:"output,type:jsonb"`
	Error       string           `bun:"error"`
	EmittedAt   time.Time        `bun:"emitted_at,notnull,default:now()"`
}

// UnitHeartbeat is the bun model for unit_heartbeats (liveness, purgeable).
type UnitHeartbeat struct {
	bun.BaseModel `bun:"table:unit_heartbeats"`

	UnitID    string    `bun:"unit_id,notnull,type:uuid"`
	EmittedAt time.Time `bun:"emitted_at,notnull,default:now()"`
}
