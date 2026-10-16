// Package db menyediakan pool PostgreSQL dan transaksi per request.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier adalah bagian pgx yang dipakai kode hasil sqlc; dipenuhi *pgxpool.Pool maupun pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, rows pgx.CopyFromSource) (int64, error)
}

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("konfigurasi basis data: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("tidak bisa terhubung ke basis data: %w", err)
	}
	return pool, nil
}

type txKey struct{}

// Conn mengembalikan transaksi yang sedang berjalan di ctx, atau pool bila tidak ada.
func Conn(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// InTx menjalankan fn dalam satu transaksi: commit bila fn sukses, rollback bila gagal atau panic.
func InTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(ctx context.Context) error) (err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run menjalankan fn di transaksi yang sudah ada di ctx, atau membuka transaksi baru bila
// belum ada. Dipakai operasi yang wajib atomik, mis. posting jurnal beserta baris-barisnya.
func Run(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	return InTx(ctx, pool, pgx.TxOptions{}, fn)
}

// IsUniqueViolation: galat pelanggaran UNIQUE (23505), mis. untuk menolak data ganda dengan pesan jelas.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// Savepoint menjalankan fn di dalam savepoint transaksi yang sedang berjalan: bila fn gagal,
// hanya perubahan fn yang dibatalkan dan transaksinya tetap bisa dipakai.
func Savepoint(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context) error) error {
	conn := Conn(ctx, pool)
	if _, err := conn.Exec(ctx, "SAVEPOINT lajur_sp"); err != nil {
		return err
	}
	if err := fn(ctx); err != nil {
		if _, rbErr := conn.Exec(ctx, "ROLLBACK TO SAVEPOINT lajur_sp"); rbErr != nil {
			return rbErr
		}
		_, _ = conn.Exec(ctx, "RELEASE SAVEPOINT lajur_sp")
		return err
	}
	_, err := conn.Exec(ctx, "RELEASE SAVEPOINT lajur_sp")
	return err
}
