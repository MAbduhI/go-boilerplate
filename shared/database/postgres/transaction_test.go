package postgres

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newMockClient returns a Client backed by sqlmock, so tests can assert the
// exact statement sequence reaching the driver.
func newMockClient(t *testing.T) (*Client, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	gormDB, err := gorm.Open(
		gormpg.New(gormpg.Config{Conn: sqlDB, DriverName: "postgres"}),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return NewWithGorm(gormDB, Config{}), mock
}

const updateSQL = `UPDATE "widgets" SET "name"=$1`

// TestWithTransactionCommitsThroughContext is the end-to-end regression test.
//
// Under the original bug, Db(ctx) inside the callback returned the pool rather
// than the transaction, so the UPDATE was issued on a separate connection
// outside BEGIN/COMMIT. sqlmock enforces ordering, so if the write escaped the
// transaction this test fails on an out-of-order expectation.
func TestWithTransactionCommitsThroughContext(t *testing.T) {
	c, mock := newMockClient(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("inside-tx").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := c.WithTransaction(context.Background(), func(ctx context.Context) error {
		// A repository would reach the handle exactly this way.
		if !InTransaction(ctx) {
			t.Error("callback ctx does not carry the transaction")
		}
		return c.Db(ctx).Exec(updateSQL, "inside-tx").Error
	})
	if err != nil {
		t.Fatalf("WithTransaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("statement sequence mismatch: %v", err)
	}
}

// TestWithTransactionRollsBackOnError checks the failure path issues ROLLBACK
// and surfaces the callback's error unchanged.
func TestWithTransactionRollsBackOnError(t *testing.T) {
	c, mock := newMockClient(t)
	sentinel := errors.New("usecase failed")

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("doomed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	err := c.WithTransaction(context.Background(), func(ctx context.Context) error {
		if err := c.Db(ctx).Exec(updateSQL, "doomed").Error; err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTransaction error = %v, want %v", err, sentinel)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("statement sequence mismatch: %v", err)
	}
}

// TestWithTransactionRollsBackOnPanic checks a panicking callback still releases
// the transaction, and that the panic is re-raised for the caller.
func TestWithTransactionRollsBackOnPanic(t *testing.T) {
	c, mock := newMockClient(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	defer func() {
		if r := recover(); r == nil {
			t.Error("panic was swallowed, want it re-raised after rollback")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("statement sequence mismatch: %v", err)
		}
	}()

	_ = c.WithTransaction(context.Background(), func(context.Context) error {
		panic("boom")
	})
}

// TestNestedTransactionUsesSavepoint proves a usecase that opens a transaction
// stays composable when called from within another one: the inner call joins
// the outer transaction through a savepoint rather than opening a second
// connection, so there is exactly one BEGIN.
func TestNestedTransactionUsesSavepoint(t *testing.T) {
	c, mock := newMockClient(t)

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("nested").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// A successful nested block issues no RELEASE SAVEPOINT: GORM only emits
	// ROLLBACK TO SAVEPOINT, on failure. The outer COMMIT subsumes the rest.
	mock.ExpectCommit()

	err := c.WithTransaction(context.Background(), func(outer context.Context) error {
		outerTx, _ := TxFromContext(outer)

		return c.WithTransaction(outer, func(inner context.Context) error {
			innerTx, ok := TxFromContext(inner)
			if !ok {
				t.Error("nested callback ctx lost the transaction")
			}
			if innerTx == outerTx {
				t.Error("nested call reused the outer handle; want a savepoint-scoped handle")
			}
			return c.Db(inner).Exec(updateSQL, "nested").Error
		})
	})
	if err != nil {
		t.Fatalf("nested WithTransaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("statement sequence mismatch: %v", err)
	}
}

// TestDbFallsBackToPool confirms that outside a transaction Db(ctx) issues on
// the pool with no BEGIN.
func TestDbFallsBackToPool(t *testing.T) {
	c, mock := newMockClient(t)

	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("no-tx").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := c.Db(context.Background()).Exec(updateSQL, "no-tx").Error; err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("statement sequence mismatch: %v", err)
	}
}

// TestNestedTransactionRollbackKeepsOuterAlive shows the payoff of savepoints:
// a failing inner block discards only its own work, and the outer transaction
// can still commit.
func TestNestedTransactionRollbackKeepsOuterAlive(t *testing.T) {
	c, mock := newMockClient(t)
	innerErr := errors.New("inner failed")

	mock.ExpectBegin()
	mock.ExpectExec("SAVEPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ROLLBACK TO SAVEPOINT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("outer-survives").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := c.WithTransaction(context.Background(), func(outer context.Context) error {
		if err := c.WithTransaction(outer, func(context.Context) error {
			return innerErr
		}); !errors.Is(err, innerErr) {
			t.Errorf("inner error = %v, want %v", err, innerErr)
		}
		// The outer transaction is still usable after the savepoint rollback.
		return c.Db(outer).Exec(updateSQL, "outer-survives").Error
	})
	if err != nil {
		t.Fatalf("outer WithTransaction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("statement sequence mismatch: %v", err)
	}
}
