//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureSQLiteRuntimeTables_CreatesUsageBillingDedupTables(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE usage_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT
		)
	`); err != nil {
		t.Fatalf("create usage_logs: %v", err)
	}
	if err := ensureSQLiteRuntimeTables(ctx, db); err != nil {
		t.Fatalf("ensureSQLiteRuntimeTables: %v", err)
	}

	assertSQLiteTableExists(t, ctx, db, "usage_billing_dedup")
	assertSQLiteTableExists(t, ctx, db, "usage_billing_dedup_archive")
	assertSQLiteIndexExists(t, ctx, db, "idx_usage_billing_dedup_request_api_key")
	assertSQLiteIndexExists(t, ctx, db, "idx_usage_billing_dedup_created_at")
	assertSQLiteIndexExists(t, ctx, db, "idx_usage_billing_dedup_archive_created_at")
}

func assertSQLiteTableExists(t *testing.T, ctx context.Context, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name = ?", table).Scan(&name)
	if err != nil {
		t.Fatalf("table %s missing: %v", table, err)
	}
	if name != table {
		t.Fatalf("table mismatch: got %q want %q", name, table)
	}
}

func assertSQLiteIndexExists(t *testing.T, ctx context.Context, db *sql.DB, index string) {
	t.Helper()
	var name string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND name = ?", index).Scan(&name)
	if err != nil {
		t.Fatalf("index %s missing: %v", index, err)
	}
	if name != index {
		t.Fatalf("index mismatch: got %q want %q", name, index)
	}
}
