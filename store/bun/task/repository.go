package task

import (
	"context"
	"encoding/json"
	"time"

	"github.com/terapps/gonveyor/store"
	"github.com/terapps/gonveyor/store/bun/bunutil"
	"github.com/uptrace/bun"
)

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

func (r *Repository) SetDispatched(ctx context.Context, taskID string) (bool, error) {
	res, err := r.db.NewUpdate().
		TableExpr("blueprint_tasks").
		Set("status = ?", store.TaskStatusDispatched).
		Set("updated_at = now()").
		Where("id = ?", taskID).
		Where("status = ?", store.TaskStatusPending).
		Exec(ctx)
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *Repository) SetRunning(ctx context.Context, taskID string) (bool, error) {
	now := time.Now()
	res, err := r.db.NewUpdate().
		TableExpr("blueprint_tasks").
		Set("status = ?", store.TaskStatusRunning).
		Set("locked_until = ?", now.Add(30*time.Second)).
		Set("updated_at = now()").
		Where("id = ?", taskID).
		Where("status = ?", store.TaskStatusDispatched).
		Exec(ctx)
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *Repository) RenewLock(ctx context.Context, taskID string) error {
	now := time.Now()
	_, err := r.db.NewUpdate().
		TableExpr("blueprint_tasks").
		Set("locked_until = ?", now.Add(30*time.Second)).
		Where("id = ?", taskID).
		Where("status = ?", store.TaskStatusRunning).
		Exec(ctx)
	return err
}

func (r *Repository) SetSuccess(ctx context.Context, taskID string, result []byte) (bool, error) {
	res, err := r.db.NewUpdate().
		TableExpr("blueprint_tasks").
		Set("status = ?", store.TaskStatusSuccess).
		Set("result = ?", json.RawMessage(result)).
		Set("updated_at = now()").
		Where("id = ?", taskID).
		Where("status = ?", store.TaskStatusRunning).
		Exec(ctx)
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *Repository) SetFailed(ctx context.Context, taskID string, errMsg string) error {
	_, err := r.db.NewUpdate().
		TableExpr("blueprint_tasks").
		Set("status = ?", store.TaskStatusFailed).
		Set("error = ?", errMsg).
		Set("updated_at = now()").
		Where("id = ?", taskID).
		Where("status = ?", store.TaskStatusRunning).
		Exec(ctx)
	return err
}

func (r *Repository) Get(ctx context.Context, taskID string) (*Task, error) {
	m := &Task{}
	err := r.db.NewSelect().Model(m).Where("id = ?", taskID).Scan(ctx)
	return m, err
}

func (r *Repository) AllByBlueprint(ctx context.Context, blueprintID string) ([]store.Task, error) {
	var tasks []Task
	err := r.db.NewSelect().Model(&tasks).Where("blueprint_id = ?", blueprintID).Scan(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]store.Task, len(tasks))
	for i, t := range tasks {
		out[i] = t.ToStore()
	}

	return out, nil
}

func (r *Repository) DepsByBlueprint(ctx context.Context, blueprintID string) ([]store.TaskDependency, error) {
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

	out := make([]store.TaskDependency, len(deps))
	for i, d := range deps {
		out[i] = store.TaskDependency{TaskID: d.TaskID, DependsOnID: d.DependsOnID}
	}

	return out, nil
}

func (r *Repository) Pending(ctx context.Context, blueprintID string) ([]store.Task, error) {
	var tasks []Task
	err := r.db.NewSelect().
		Model(&tasks).
		Where("blueprint_id = ?", blueprintID).
		Where("status = ?", store.TaskStatusPending).
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]store.Task, len(tasks))
	for i, t := range tasks {
		out[i] = t.ToStore()
	}

	return out, nil
}

func (r *Repository) GatherDepResults(ctx context.Context, taskID string) (map[string][]json.RawMessage, error) {
	type row struct {
		Key    string          `bun:"key"`
		Result json.RawMessage `bun:"result"`
	}

	var rows []row

	err := r.db.NewSelect().
		TableExpr("blueprint_task_dependencies d").
		ColumnExpr("dep.key, dep.result").
		Join("JOIN blueprint_tasks dep ON dep.id = d.depends_on_id").
		Where("d.task_id = ?", taskID).
		Where("dep.status = ?", store.TaskStatusSuccess).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]json.RawMessage, len(rows))
	for _, r := range rows {
		out[r.Key] = append(out[r.Key], r.Result)
	}

	return out, nil
}

func (r *Repository) Next(ctx context.Context, completedTaskID string) ([]store.Task, error) {
	var tasks []Task

	err := r.db.NewSelect().
		TableExpr("blueprint_tasks AS t").
		ColumnExpr("t.*").
		Join("JOIN blueprint_task_dependencies d ON d.task_id = t.id").
		Where("d.depends_on_id = ?", completedTaskID).
		Where("t.status = ?", store.TaskStatusPending).
		Where("NOT EXISTS (?)",
			r.db.NewSelect().
				ColumnExpr("1").
				TableExpr("blueprint_task_dependencies d2").
				Join("JOIN blueprint_tasks dep ON dep.id = d2.depends_on_id").
				Where("d2.task_id = t.id").
				Where("dep.status != ?", store.TaskStatusSuccess),
		).
		Scan(ctx, &tasks)
	if err != nil {
		return nil, err
	}

	out := make([]store.Task, len(tasks))
	for i, t := range tasks {
		out[i] = t.ToStore()
	}

	return out, nil
}
