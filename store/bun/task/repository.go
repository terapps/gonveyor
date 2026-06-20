package task

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/store/bun/bunutil"
	"github.com/uptrace/bun"
)

// errAlreadyCompleted is a sentinel returned inside SetSuccess when the unique
// index on task_completed fires (idempotent redelivery). It causes RunInTx to
// rollback, and is swallowed by the caller which returns (false, nil, nil).
var errAlreadyCompleted = errors.New("task already completed")

type Repository struct {
	db *bun.DB
}

func New(db *bun.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&tasks).Exec(ctx)
	return err
}

func (r *Repository) InsertDependencies(ctx context.Context, deps []*Dependency) error {
	if len(deps) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&deps).Exec(ctx)
	return err
}

func (r *Repository) InsertEvents(ctx context.Context, events []*TaskEvent) error {
	if len(events) == 0 {
		return nil
	}
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(&events).Exec(ctx)
	return err
}

func (r *Repository) SetDispatched(ctx context.Context, taskID string) (bool, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_events (task_id, blueprint_id, key, type)
		SELECT id, blueprint_id, key, 'task_dispatched'
		FROM blueprint_tasks WHERE id = ?
	`, taskID)
	return err == nil, err
}

func (r *Repository) SetRunning(ctx context.Context, taskID string) (bool, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_events (task_id, blueprint_id, key, type)
		SELECT id, blueprint_id, key, 'task_started'
		FROM blueprint_tasks WHERE id = ?
	`, taskID)
	return err == nil, err
}

func (r *Repository) SetFailed(ctx context.Context, taskID string, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO task_events (task_id, blueprint_id, key, type, error)
		SELECT id, blueprint_id, key, 'task_failed', ?
		FROM blueprint_tasks WHERE id = ?
	`, errMsg, taskID)
	return err
}

// SetSuccess atomically records task_completed, decrements pending_deps on
// successors, and dispatches any newly unblocked tasks (pending_deps = 0 with
// no prior dispatch or completion event). Returns (false, nil, nil) on
// idempotent redelivery.
func (r *Repository) SetSuccess(ctx context.Context, taskID string, result json.RawMessage) (bool, []ledger.Task, error) {
	var unblocked []ledger.Task

	err := bunutil.RunInTx(ctx, r.db, func(ctx context.Context) error {
		db := bunutil.IDB(ctx, r.db)

		// 1. Insert task_completed (partial-index unique guard handles redelivery).
		res, err := db.ExecContext(ctx, `
			INSERT INTO task_events (task_id, blueprint_id, key, type, output)
			SELECT id, blueprint_id, key, 'task_completed', ?::jsonb
			FROM blueprint_tasks WHERE id = ?
			ON CONFLICT (task_id, type) WHERE type = 'task_completed' DO NOTHING
		`, string(result), taskID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errAlreadyCompleted
		}

		// 2. Decrement pending_deps for all direct successors.
		_, err = db.ExecContext(ctx, `
			UPDATE blueprint_tasks
			SET pending_deps = pending_deps - 1, updated_at = now()
			WHERE id IN (
				SELECT task_id FROM blueprint_task_dependencies WHERE depends_on_id = ?
			)
		`, taskID)
		if err != nil {
			return err
		}

		// 3. Select newly unblocked tasks (FOR UPDATE prevents double-dispatch
		//    when two deps complete concurrently).
		var rows []taskRow
		err = db.NewSelect().
			TableExpr("blueprint_tasks t").
			ColumnExpr("t.*, b.name AS blueprint_name").
			Join("JOIN blueprints b ON b.id = t.blueprint_id").
			Where("t.id IN (SELECT task_id FROM blueprint_task_dependencies WHERE depends_on_id = ?)", taskID).
			Where("t.pending_deps = 0").
			Where("NOT EXISTS (SELECT 1 FROM task_events WHERE task_id = t.id AND type IN ('task_dispatched', 'task_completed'))").
			For("UPDATE OF t").
			Scan(ctx, &rows)
		if err != nil {
			return err
		}

		if len(rows) == 0 {
			return nil
		}

		// 4. Mark unblocked tasks as dispatched.
		events := make([]*TaskEvent, len(rows))
		for i, row := range rows {
			events[i] = &TaskEvent{
				TaskID:      row.ID,
				BlueprintID: row.BlueprintID,
				Key:         row.Key,
				Type:        EventTaskDispatched,
			}
		}
		_, err = db.NewInsert().Model(&events).Exec(ctx)
		if err != nil {
			return err
		}

		unblocked = make([]ledger.Task, len(rows))
		for i, row := range rows {
			unblocked[i] = row.toLedger()
		}

		return nil
	})

	if errors.Is(err, errAlreadyCompleted) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}

	return true, unblocked, nil
}

func (r *Repository) RenewLock(ctx context.Context, taskID string) error {
	_, err := r.db.NewInsert().
		Model(&TaskHeartbeat{TaskID: taskID}).
		Exec(ctx)
	return err
}

func (r *Repository) Get(ctx context.Context, taskID string) (ledger.Task, error) {
	var row taskRow
	err := bunutil.IDB(ctx, r.db).NewSelect().
		TableExpr("blueprint_tasks t").
		ColumnExpr("t.*, b.name AS blueprint_name").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Where("t.id = ?", taskID).
		Scan(ctx, &row)
	return row.toLedger(), err
}

func (r *Repository) AllByBlueprint(ctx context.Context, blueprintID string) ([]ledger.Task, error) {
	var rows []taskRow
	err := r.db.NewSelect().
		TableExpr("blueprint_tasks t").
		ColumnExpr("t.*, b.name AS blueprint_name").
		Join("JOIN blueprints b ON b.id = t.blueprint_id").
		Where("t.blueprint_id = ?", blueprintID).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Task, len(rows))
	for i, row := range rows {
		out[i] = row.toLedger()
	}
	return out, nil
}

func (r *Repository) DepsByBlueprint(ctx context.Context, blueprintID string) ([]ledger.TaskDependency, error) {
	var deps []Dependency
	err := r.db.NewSelect().
		TableExpr("blueprint_task_dependencies d").
		ColumnExpr("d.*").
		Join("JOIN blueprint_tasks t ON t.id = d.task_id").
		Where("t.blueprint_id = ?", blueprintID).
		Scan(ctx, &deps)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.TaskDependency, len(deps))
	for i, d := range deps {
		out[i] = ledger.TaskDependency{TaskID: d.TaskID, DependsOnID: d.DependsOnID}
	}
	return out, nil
}

func (r *Repository) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	type row struct {
		Key    string          `bun:"key"`
		Output json.RawMessage `bun:"output"`
	}

	var rows []row
	err := r.db.NewSelect().
		TableExpr("blueprint_task_dependencies d").
		ColumnExpr("e.key, e.output").
		Join("JOIN task_events e ON e.task_id = d.depends_on_id AND e.type = 'task_completed'").
		Where("d.task_id = ?", taskID).
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
