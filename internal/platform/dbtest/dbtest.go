// Package dbtest membuat basis data PostgreSQL sementara untuk tes integrasi.
package dbtest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/db"
	"lajur/internal/platform/migrate"
	"lajur/internal/platform/rbac"
)

// URL membuat basis data kosong dari LAJUR_TEST_DATABASE_URL dan menghapusnya setelah tes.
// Tes dilewati bila variabel itu kosong.
func URL(t testing.TB) string {
	t.Helper()
	admin := os.Getenv("LAJUR_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("LAJUR_TEST_DATABASE_URL kosong; tes integrasi dilewati")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	name := "lajur_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// Pool membuat basis data sementara yang sudah dimigrasi dan berisi peran bawaan.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := PoolURL(t)
	return pool
}

// PoolURL seperti Pool, ditambah alamat basis datanya (mis. untuk pg_dump di tes backup).
func PoolURL(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	dbURL := URL(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := rbac.SyncBuiltinRoles(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, dbURL
}
