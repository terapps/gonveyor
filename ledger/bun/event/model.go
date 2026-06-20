package event

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/ledger"
	"github.com/uptrace/bun"
)

// NodeEvent is the bun model for node_events (append-only).
// Output is populated for node_completed; Error for node_failed.
type NodeEvent struct {
	bun.BaseModel `bun:"table:node_events"`

	ID          int64            `bun:"id,pk,autoincrement"`
	NodeID      string           `bun:"node_id,notnull,type:uuid"`
	BlueprintID string           `bun:"blueprint_id,notnull,type:uuid"`
	Key         string           `bun:"key,notnull"`
	Type        ledger.EventType `bun:"type,notnull"`
	Output      json.RawMessage `bun:"output,type:jsonb"`
	Error       string          `bun:"error"`
	EmittedAt   time.Time       `bun:"emitted_at,notnull,default:now()"`
}

// NodeHeartbeat is the bun model for node_heartbeats (liveness, purgeable).
type NodeHeartbeat struct {
	bun.BaseModel `bun:"table:node_heartbeats"`

	NodeID    string    `bun:"node_id,notnull,type:uuid"`
	EmittedAt time.Time `bun:"emitted_at,notnull,default:now()"`
}
