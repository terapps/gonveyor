package node

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/ledger"
	"github.com/uptrace/bun"
)

const (
	NodeTypeTask   = "task"
	NodeTypeSignal = "signal"
)

// Node is the bun model for blueprint_nodes.
type Node struct {
	bun.BaseModel `bun:"table:blueprint_nodes"`

	ID          string          `bun:"id,pk,type:uuid"`
	BlueprintID string          `bun:"blueprint_id,notnull"`
	Key         string          `bun:"key,notnull"`
	NodeType    string          `bun:"node_type,notnull,default:'task'"`
	PendingDeps int             `bun:"pending_deps,notnull"`
	Payload     json.RawMessage `bun:"payload,type:jsonb"`
	CreatedAt   time.Time       `bun:"created_at,notnull,default:now()"`
	UpdatedAt   time.Time       `bun:"updated_at,notnull,default:now()"`
}

// nodeRow holds a Node joined with its blueprint name.
type nodeRow struct {
	Node
	BlueprintName string `bun:"blueprint_name"`
}

func (r nodeRow) toLedger() ledger.Node {
	return ledger.Node{
		ID:            r.ID,
		BlueprintID:   r.BlueprintID,
		BlueprintName: r.BlueprintName,
		Key:           r.Key,
		NodeType:      r.NodeType,
		Payload:       r.Payload,
	}
}

// Dependency is the bun model for blueprint_node_dependencies.
type Dependency struct {
	bun.BaseModel `bun:"table:blueprint_node_dependencies"`

	NodeID      string `bun:"node_id,pk"`
	DependsOnID string `bun:"depends_on_id,pk"`
}
