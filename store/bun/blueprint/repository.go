package blueprint

import (
	"context"

	"github.com/terapps/gonveyor/store/bun/bunutil"
	"github.com/uptrace/bun"
)

type Repository struct {
	db *bun.DB
}

func New(db *bun.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, m *Blueprint) error {
	_, err := bunutil.IDB(ctx, r.db).NewInsert().Model(m).Exec(ctx)
	return err
}

func (r *Repository) Get(ctx context.Context, blueprintID string) (*Blueprint, error) {
	m := &Blueprint{}
	err := r.db.NewSelect().Model(m).Where("id = ?", blueprintID).Scan(ctx)
	return m, err
}

func (r *Repository) List(ctx context.Context) ([]*Blueprint, error) {
	var ms []*Blueprint
	err := r.db.NewSelect().Model(&ms).OrderExpr("created_at DESC").Scan(ctx)
	return ms, err
}

func (r *Repository) SetDispatched(ctx context.Context, blueprintID string) error {
	_, err := r.db.NewUpdate().
		TableExpr("blueprints").
		Set("dispatched_at = now()").
		Where("id = ?", blueprintID).
		Where("dispatched_at IS NULL").
		Exec(ctx)
	return err
}
