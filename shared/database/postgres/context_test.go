package postgres

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// TestContextKeyRoundTrip is the direct regression test for the inherited bug:
// the transaction was stored under a struct key and read back with the string
// "txContext", so the lookup could never succeed.
func TestContextKeyRoundTrip(t *testing.T) {
	tx := &gorm.DB{}
	ctx := contextWithTx(context.Background(), tx)

	got, ok := TxFromContext(ctx)
	if !ok {
		t.Fatal("TxFromContext reported no transaction after contextWithTx stored one")
	}
	if got != tx {
		t.Fatalf("TxFromContext returned a different handle: got %p, want %p", got, tx)
	}
	if !InTransaction(ctx) {
		t.Error("InTransaction = false, want true")
	}
}

// TestLegacyStringKeyDoesNotResolve pins the shape of the original defect: a
// string key is a distinct key from txKey{}, so a lookup by string misses even
// when a transaction is present. It guards against anyone reintroducing a
// string-keyed read.
func TestLegacyStringKeyDoesNotResolve(t *testing.T) {
	ctx := contextWithTx(context.Background(), &gorm.DB{})

	if v := ctx.Value("txContext"); v != nil {
		t.Fatalf(`ctx.Value("txContext") = %v, want nil`, v)
	}
	// And the inverse: a transaction stashed under the string key must not be
	// picked up as the ambient transaction.
	//nolint:staticcheck,revive // a string key is exactly the defect under test
	strCtx := context.WithValue(context.Background(), "txContext", &gorm.DB{})
	if _, ok := TxFromContext(strCtx); ok {
		t.Fatal("TxFromContext accepted a string-keyed value")
	}
}

func TestTxFromContextEmpty(t *testing.T) {
	if _, ok := TxFromContext(context.Background()); ok {
		t.Fatal("TxFromContext found a transaction in a bare context")
	}
	if InTransaction(context.Background()) {
		t.Error("InTransaction = true for a bare context")
	}
}

// TestTxFromContextRejectsNil guards Db(ctx) against returning a nil handle if
// a nil transaction ever reaches the context slot.
func TestTxFromContextRejectsNil(t *testing.T) {
	ctx := contextWithTx(context.Background(), nil)
	if _, ok := TxFromContext(ctx); ok {
		t.Fatal("TxFromContext accepted a nil transaction")
	}
}

// TestDbPrefersAmbientTransaction proves the consumer-visible half of the fix:
// a repository calling Db(ctx) inside a transaction receives the transaction,
// not the pool.
func TestDbPrefersAmbientTransaction(t *testing.T) {
	tx := &gorm.DB{}
	c := &Client{db: &gorm.DB{}}

	if got := c.Db(contextWithTx(context.Background(), tx)); got != tx {
		t.Fatalf("Db returned the pool handle instead of the ambient transaction: got %p, want %p", got, tx)
	}
}
