package bunutil

import (
	"context"

	"github.com/uptrace/bun"
)

type contextKey struct{}

var txKey = contextKey{}

// RunInTx runs fn within a database transaction, stored in the returned
// context. Repositories should call IDB(ctx, db) to transparently use the
// transaction when present. The transaction commits if fn returns nil, and
// rolls back otherwise.
func RunInTx(ctx context.Context, db *bun.DB, fn func(ctx context.Context) error) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return fn(context.WithValue(ctx, txKey, tx))
	})
}

// IDB returns the transaction stored in ctx by RunInTx, or fallback if there
// is none.
func IDB(ctx context.Context, fallback bun.IDB) bun.IDB {
	if tx, ok := ctx.Value(txKey).(bun.Tx); ok {
		return tx
	}
	return fallback
}
