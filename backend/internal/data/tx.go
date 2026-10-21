package data

import (
	"context"

	"gorm.io/gorm"
)

type txKey struct{}

// Tx hands the caller a context carrying the transaction. Every query goes
// through Conn, so calls inside join it without being told.
type Tx struct{ db *gorm.DB }

func NewTx(db *gorm.DB) *Tx { return &Tx{db: db} }

func (t *Tx) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// Conn is the connection a query should use: the open transaction if there is
// one, otherwise the pool.
func Conn(ctx context.Context, base *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return base.WithContext(ctx)
}
