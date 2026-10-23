package data

import (
	"context"
	"sync"

	"gorm.io/gorm"
)

type txKey struct{}

type afterKey struct{}

// Tx hands the caller a context carrying the transaction. Every query goes
// through Conn, so calls inside join it without being told.
type Tx struct{ db *gorm.DB }

func NewTx(db *gorm.DB) *Tx { return &Tx{db: db} }

// Do runs fn in one transaction, then, once it has committed, what fn left
// with AfterCommit, in order. A rollback runs none of it.
func (t *Tx) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	after := &afterCommit{}
	err := t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(context.WithValue(ctx, txKey{}, tx), afterKey{}, after))
	})
	if err != nil {
		return err
	}
	after.run()
	return nil
}

// Conn is the connection a query should use: the open transaction if there is
// one, otherwise the pool.
func Conn(ctx context.Context, base *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return base.WithContext(ctx)
}

// Outside is ctx without the transaction it carries, for work that runs once
// that transaction has committed (AfterCommit) and opens its own: a query
// through Conn uses the pool again, and a Tx.Do inside it a transaction of
// its own.
func Outside(ctx context.Context) context.Context {
	return context.WithValue(context.WithValue(ctx, txKey{}, nil), afterKey{}, nil)
}

// AfterCommit runs fn once the transaction ctx carries has committed, and
// never if it rolls back: what tells the outside world about a write (a push
// to a phone) must not tell it about one that did not happen. Outside a
// transaction the write has already happened, so fn runs at once. fn runs on
// the caller's goroutine, so it must be quick and must not fail the write.
func AfterCommit(ctx context.Context, fn func()) {
	if after, ok := ctx.Value(afterKey{}).(*afterCommit); ok {
		after.add(fn)
		return
	}
	fn()
}

// afterCommit is what one transaction runs once it has committed.
type afterCommit struct {
	mu  sync.Mutex
	fns []func()
}

func (a *afterCommit) add(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fns = append(a.fns, fn)
}

func (a *afterCommit) run() {
	a.mu.Lock()
	fns := a.fns
	a.fns = nil
	a.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
