package unit

import (
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/ledger"
	"github.com/uptrace/bun"
)

const (
	UnitTypeTask   = "task"
	UnitTypeSignal = "signal"
)

// Unit is the bun model for blueprint_units.
type Unit struct {
	bun.BaseModel `bun:"table:blueprint_units"`

	ID          string    `bun:"id,pk,type:uuid"`
	BlueprintID string    `bun:"blueprint_id,notnull"`
	Key         string    `bun:"key,notnull"`
	UnitType    string    `bun:"unit_type,notnull,default:'task'"`
	PendingDeps int       `bun:"pending_deps,notnull"`
	CreatedAt   time.Time `bun:"created_at,notnull,default:now()"`
	UpdatedAt   time.Time `bun:"updated_at,notnull,default:now()"`
}

// unitRow holds a Unit joined with its blueprint name and optional seed payload.
type unitRow struct {
	Unit
	BlueprintName string          `bun:"blueprint_name"`
	SeedPayload   json.RawMessage `bun:"seed_payload"`
}

func (r unitRow) toLedger() ledger.Unit {
	return ledger.Unit{
		ID:            r.ID,
		BlueprintID:   r.BlueprintID,
		BlueprintName: r.BlueprintName,
		Key:           r.Key,
		UnitType:      r.UnitType,
		Payload:       r.SeedPayload,
	}
}

// Dependency is the bun model for blueprint_unit_dependencies.
type Dependency struct {
	bun.BaseModel `bun:"table:blueprint_unit_dependencies"`

	UnitID      string `bun:"unit_id,pk"`
	DependsOnID string `bun:"depends_on_id,pk"`
}
