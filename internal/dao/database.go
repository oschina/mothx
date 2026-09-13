package dao

import (
	"context"
	"database/sql"
	"sync"

	"github.com/uptrace/bun"

	database "github.com/startvibecoding/mothx/internal/db"
)

// Database is the DAO-facing handle to a managed Bun connection. Managed
// connection ownership belongs exclusively to internal/db.
type Database struct {
	db      *bun.DB
	managed bool
}

// Tx is the transaction handle shared by DAO-backed session helpers. It is a
// Bun transaction, so callers never own or close the underlying connection.
type Tx = bun.Tx
type Executor = bun.IDB
type Row = sql.Row
type Rows = sql.Rows
type TxOptions = sql.TxOptions
type NullString = sql.NullString
type NullInt64 = sql.NullInt64

var ErrNoRows = sql.ErrNoRows

var managedHandles sync.Map

func WrapDatabase(database *bun.DB) *Database {
	if database == nil {
		return nil
	}
	if existing, ok := managedHandles.Load(database); ok {
		return existing.(*Database)
	}
	handle := &Database{db: database}
	handle.managed = true
	actual, _ := managedHandles.LoadOrStore(database, handle)
	return actual.(*Database)
}

// WrapStandaloneDatabase wraps a caller-owned Bun connection. It exists for
// offline checks that intentionally open a second connection; normal runtime
// code must use WrapDatabase and CloseAll.
func WrapStandaloneDatabase(database *bun.DB) *Database {
	if database == nil {
		return nil
	}
	return &Database{db: database}
}

// Bun returns the underlying ORM connection for DAO implementations.
func (d *Database) Bun() *bun.DB {
	if d == nil {
		return nil
	}
	return d.db
}

// Close only closes explicitly standalone connections. Managed connections
// are owned by internal/db and must be released through CloseAll.
func (d *Database) Close() error {
	if d == nil || d.managed || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Begin and BeginTx create Bun transactions from the managed connection. The
// pointer form keeps existing helper signatures stable while ownership stays
// with the managed database.
//
// Both retry a SQLITE_BUSY/SQLITE_LOCKED begin within the shared budget
// (internal/db): the DSN begins transactions with BEGIN IMMEDIATE, so a busy
// writer can outlast the connection's busy_timeout without the database being
// broken.
func (d *Database) Begin() (*Tx, error) {
	tx, err := database.BeginTx(context.Background(), d.db, nil)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (d *Database) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := database.BeginTx(ctx, d.db, opts)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

// RunInTx executes a callback against a Bun transaction on the managed
// connection. This is the canonical transaction boundary for session code, and
// it retries a transient busy begin like Begin/BeginTx do.
func (d *Database) RunInTx(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, Tx) error) error {
	return database.RunInTx(ctx, d.db, opts, fn)
}
