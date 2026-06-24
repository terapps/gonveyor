package event

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/terapps/gonveyor/events"
	"github.com/terapps/gonveyor/ledger/bun/bunutil"
	"github.com/uptrace/bun"
)

type Repository struct {
	db *bun.DB
}

func New(db *bun.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, events []*UnitEvent) error {
	if len(events) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&events).Exec(ctx)
	return err
}

// RecordCompleted inserts the unit_completed event. Returns false (nil error) if the
// unit was already completed (idempotent redelivery guard via partial unique index).
// Must be called within a transaction — uses bunutil.IDB to pick up the tx from ctx.
func (r *Repository) RecordCompleted(ctx context.Context, unitID string, result json.RawMessage) (bool, error) {
	// ON CONFLICT predicate must match the partial index — interpolated, not parametrized.
	res, err := bunutil.IDB(ctx, r.db).ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO unit_events (unit_id, blueprint_id, key, type, output)
		SELECT id, blueprint_id, key, ?, ?::jsonb
		FROM blueprint_units WHERE id = ?
		ON CONFLICT (unit_id, type) WHERE type = '%s' DO NOTHING
	`, events.EventUnitCompleted), events.EventUnitCompleted, string(result), unitID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *Repository) RecordStarted(ctx context.Context, unitID string, payload json.RawMessage) (bool, error) {
	var p *string
	if len(payload) > 0 {
		s := string(payload)
		p = &s
	}
	_, err := bunutil.IDB(ctx, r.db).ExecContext(ctx, `
		INSERT INTO unit_events (unit_id, blueprint_id, key, type, output)
		SELECT id, blueprint_id, key, ?, ?::jsonb
		FROM blueprint_units WHERE id = ?
	`, events.EventUnitStarted, p, unitID)
	return err == nil, err
}

func (r *Repository) RecordFailed(ctx context.Context, unitID string, errMsg string) error {
	_, err := bunutil.IDB(ctx, r.db).ExecContext(ctx, `
		INSERT INTO unit_events (unit_id, blueprint_id, key, type, error)
		SELECT id, blueprint_id, key, ?, ?
		FROM blueprint_units WHERE id = ?
	`, events.EventUnitFailed, errMsg, unitID)
	return err
}

func (r *Repository) RecordHeartbeat(ctx context.Context, unitID string) error {
	_, err := bunutil.IDB(ctx, r.db).NewInsert().
		Model(&UnitHeartbeat{UnitID: unitID}).
		Exec(ctx)
	return err
}

func (r *Repository) GatherDepResults(ctx context.Context, unitID string) (map[string][]json.RawMessage, error) {
	type row struct {
		Key    string          `bun:"key"`
		Output json.RawMessage `bun:"output"`
	}

	var rows []row
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_unit_dependencies d").
		ColumnExpr("e.key, e.output").
		Join("JOIN unit_events e ON e.unit_id = d.depends_on_id AND e.type = ?", events.EventUnitCompleted).
		Where("d.unit_id = ?", unitID).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]json.RawMessage, len(rows))
	for _, r := range rows {
		out[r.Key] = append(out[r.Key], r.Output)
	}
	return out, nil
}
