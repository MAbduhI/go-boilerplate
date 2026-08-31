package postgres

import (
	"context"

	"gorm.io/gorm"
)

// txKey is the one and only key under which a transaction is stored in a
// context. Both contextWithTx and TxFromContext use it, and nothing else in the
// repo may read or write the transaction slot.
//
// This is the fix for the bug inherited from the reference project, which
// stored the transaction under a private `ctxKey{}` struct but read it back
// with the string literal "txContext". Because those are different keys, the
// lookup always missed: Db(ctx) handed back the root *gorm.DB, so every
// repository call inside WithTransaction ran on its own connection outside the
// transaction. Writes were committed independently and a rollback silently left
// earlier writes in place.
//
// An unexported empty struct type is the correct shape for a context key: it is
// unique per package by construction, so it cannot collide with a key set by
// any other package, and it cannot be forged from outside this file.
type txKey struct{}

// contextWithTx returns a child context carrying tx.
func contextWithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext returns the transaction carried by ctx, if any.
func TxFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok && tx != nil
}

// InTransaction reports whether ctx is already inside a transaction. Useful for
// a usecase that must assert it was called within one.
func InTransaction(ctx context.Context) bool {
	_, ok := TxFromContext(ctx)
	return ok
}
