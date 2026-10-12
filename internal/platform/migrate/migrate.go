// Package migrate menjalankan migrasi basis data yang tertanam di binary.
package migrate

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"lajur/migrations"
)

// Up menerapkan semua migrasi yang belum dijalankan. Dipanggil setiap kali aplikasi start.
func Up(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("menyiapkan migrasi: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("menjalankan migrasi: %w", err)
	}
	return nil
}
