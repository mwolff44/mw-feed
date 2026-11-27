// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// dbtx is what both *sql.DB and *sql.Tx provide.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// queries holds the typed queries. They run on the store directly, or inside a
// transaction through Tx.
type queries struct{ db dbtx }

// Tx is a transaction. Inside InTx, use only the Tx: the store has a single
// connection, so calling the Store itself would wait for the transaction forever.
type Tx struct{ *queries }

// InTx runs fn in a transaction: it commits if fn returns nil, else rolls back.
func (s *Store) InTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&Tx{&queries{db: tx}}); err != nil {
		return err
	}
	return tx.Commit()
}

// unixOrNull converts a time to a nullable unix timestamp: the zero time is NULL.
func unixOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

// timeFromNull converts a nullable unix timestamp back to a time (zero if NULL).
func timeFromNull(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}
