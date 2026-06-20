package blueprint

import (
	"context"

	"github.com/terapps/gonveyor/ledger/bun/bunutil"
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
