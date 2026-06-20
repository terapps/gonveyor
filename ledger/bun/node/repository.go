package node

import (
	"context"

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

func (r *Repository) Insert(ctx context.Context, nodes []*Node) error {
	if len(nodes) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&nodes).Exec(ctx)
	return err
}

func (r *Repository) InsertDependencies(ctx context.Context, deps []*Dependency) error {
	if len(deps) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&deps).Exec(ctx)
	return err
}

func (r *Repository) Get(ctx context.Context, nodeID string) (ledger.Node, error) {
	var row nodeRow
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_nodes t").
		ColumnExpr("t.*, b.name AS blueprint_name").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Where("t.id = ?", nodeID).
		Scan(ctx, &row)
	return row.toLedger(), err
}

// DecrementPendingDeps decrements pending_deps for all direct successors of nodeID.
// Must be called within a transaction — uses bunutil.IDB to pick up the tx from ctx.
func (r *Repository) DecrementPendingDeps(ctx context.Context, nodeID string) error {
	_, err := bunutil.IDB(ctx, r.db).ExecContext(ctx, `
		UPDATE blueprint_nodes
		SET pending_deps = pending_deps - 1, updated_at = now()
		WHERE id IN (
			SELECT node_id FROM blueprint_node_dependencies WHERE depends_on_id = ?
		)
	`, nodeID)
	return err
}

// SelectUnblocked returns task nodes that became ready after nodeID completed:
// pending_deps = 0, no prior dispatch or completion event, not signal nodes.
// FOR UPDATE prevents double-dispatch when two deps complete concurrently.
// Must be called within a transaction — uses bunutil.IDB to pick up the tx from ctx.
func (r *Repository) SelectUnblocked(ctx context.Context, nodeID string) ([]ledger.Node, error) {
	var rows []nodeRow
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_nodes t").
		ColumnExpr("t.*, b.name AS blueprint_name").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Where("t.id IN (SELECT node_id FROM blueprint_node_dependencies WHERE depends_on_id = ?)", nodeID).
		Where("t.pending_deps = 0").
		Where("t.node_type = ?", NodeTypeTask).
		Where("NOT EXISTS (SELECT 1 FROM node_events WHERE node_id = t.id AND type IN (?, ?))", ledger.EventNodeDispatched, ledger.EventNodeCompleted).
		For("UPDATE OF t").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Node, len(rows))
	for i, row := range rows {
		out[i] = row.toLedger()
	}
	return out, nil
}

// FindSignalNode returns the signal node for (blueprintID, signalKey) if it has not
// yet been completed. Returns sql.ErrNoRows if not found or already completed.
func (r *Repository) FindSignalNode(ctx context.Context, blueprintID, signalKey string) (ledger.Node, error) {
	var row nodeRow
	err := r.db.NewSelect().
		TableExpr("blueprint_nodes t").
		ColumnExpr("t.*, b.name AS blueprint_name").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Where("t.blueprint_id = ?", blueprintID).
		Where("t.node_type = ?", NodeTypeSignal).
		Where("t.key = ?", signalKey).
		Where("NOT EXISTS (SELECT 1 FROM node_events WHERE node_id = t.id AND type = ?)", ledger.EventNodeCompleted).
		Scan(ctx, &row)
	return row.toLedger(), err
}
