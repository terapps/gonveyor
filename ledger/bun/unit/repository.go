package unit

import (
	"context"

	"github.com/terapps/gonveyor/events"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/ledger/bun/bunutil"
	"github.com/uptrace/bun"
)

type Repository struct {
	db *bun.DB
}

func New(db *bun.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, units []*Unit) error {
	if len(units) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&units).Exec(ctx)
	return err
}

func (r *Repository) InsertDependencies(ctx context.Context, deps []*Dependency) error {
	if len(deps) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&deps).Exec(ctx)
	return err
}

func (r *Repository) Get(ctx context.Context, unitID string) (ledger.Unit, error) {
	var row unitRow
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_units t").
		ColumnExpr("t.*, b.name AS blueprint_name, se.output AS seed_payload").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Join("LEFT JOIN unit_events se ON se.unit_id = t.id AND se.type = ?", events.EventUnitSeeded).
		Where("t.id = ?", unitID).
		Scan(ctx, &row)
	return row.toLedger(), err
}

// DecrementPendingDeps decrements pending_deps for all direct successors of unitID.
// Must be called within a transaction — uses bunutil.IDB to pick up the tx from ctx.
func (r *Repository) DecrementPendingDeps(ctx context.Context, unitID string) error {
	_, err := bunutil.IDB(ctx, r.db).ExecContext(ctx, `
		UPDATE blueprint_units
		SET pending_deps = pending_deps - 1, updated_at = now()
		WHERE id IN (
			SELECT unit_id FROM blueprint_unit_dependencies WHERE depends_on_id = ?
		)
	`, unitID)
	return err
}

// SelectUnblocked returns task units that became ready after unitID completed:
// pending_deps = 0, no prior dispatch or completion event, not signal units.
// FOR UPDATE prevents double-dispatch when two deps complete concurrently.
// Must be called within a transaction — uses bunutil.IDB to pick up the tx from ctx.
func (r *Repository) SelectUnblocked(ctx context.Context, unitID string) ([]ledger.Unit, error) {
	var rows []unitRow
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_units t").
		ColumnExpr("t.*, b.name AS blueprint_name, se.output AS seed_payload").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Join("LEFT JOIN unit_events se ON se.unit_id = t.id AND se.type = ?", events.EventUnitSeeded).
		Where("t.id IN (SELECT unit_id FROM blueprint_unit_dependencies WHERE depends_on_id = ?)", unitID).
		Where("t.pending_deps = 0").
		Where("t.unit_type = ?", UnitTypeTask).
		Where("NOT EXISTS (SELECT 1 FROM unit_events WHERE unit_id = t.id AND type IN (?, ?))", events.EventUnitDispatched, events.EventUnitCompleted).
		For("UPDATE OF t").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Unit, len(rows))
	for i, row := range rows {
		out[i] = row.toLedger()
	}
	return out, nil
}

// FindSignalUnit returns the signal unit for (blueprintID, signalKey) if it has not
// yet been completed. Returns sql.ErrNoRows if not found or already completed.
func (r *Repository) FindSignalUnit(ctx context.Context, blueprintID, signalKey string) (ledger.Unit, error) {
	var row unitRow
	err := r.db.NewSelect().
		TableExpr("blueprint_units t").
		ColumnExpr("t.*, b.name AS blueprint_name, se.output AS seed_payload").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Join("LEFT JOIN unit_events se ON se.unit_id = t.id AND se.type = ?", events.EventUnitSeeded).
		Where("t.blueprint_id = ?", blueprintID).
		Where("t.unit_type = ?", UnitTypeSignal).
		Where("t.key = ?", signalKey).
		Where("t.pending_deps = 0").
		Where("NOT EXISTS (SELECT 1 FROM unit_events WHERE unit_id = t.id AND type = ?)", events.EventUnitCompleted).
		Scan(ctx, &row)
	return row.toLedger(), err
}
