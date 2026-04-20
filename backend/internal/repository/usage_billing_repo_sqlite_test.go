//go:build unit

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/service"

	_ "modernc.org/sqlite"
)

func TestUsageBillingRepositoryApply_SQLite_EndToEnd(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db, err := sql.Open("sqlite", "file:usage_billing_repo_sqlite?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE usage_logs (id INTEGER PRIMARY KEY AUTOINCREMENT);
		CREATE TABLE groups (
			id INTEGER PRIMARY KEY,
			deleted_at TIMESTAMP NULL
		);
		CREATE TABLE user_subscriptions (
			id INTEGER PRIMARY KEY,
			group_id INTEGER NOT NULL,
			deleted_at TIMESTAMP NULL,
			daily_usage_usd REAL NOT NULL DEFAULT 0,
			weekly_usage_usd REAL NOT NULL DEFAULT 0,
			monthly_usage_usd REAL NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NULL
		);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			balance REAL NOT NULL DEFAULT 0,
			deleted_at TIMESTAMP NULL,
			updated_at TIMESTAMP NULL
		);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY,
			key TEXT NOT NULL,
			status TEXT NOT NULL,
			quota REAL NOT NULL DEFAULT 0,
			quota_used REAL NOT NULL DEFAULT 0,
			usage_5h REAL NOT NULL DEFAULT 0,
			usage_1d REAL NOT NULL DEFAULT 0,
			usage_7d REAL NOT NULL DEFAULT 0,
			window_5h_start TIMESTAMP NULL,
			window_1d_start TIMESTAMP NULL,
			window_7d_start TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL,
			updated_at TIMESTAMP NULL
		);
		CREATE TABLE accounts (
			id INTEGER PRIMARY KEY,
			extra TEXT NULL,
			deleted_at TIMESTAMP NULL,
			updated_at TIMESTAMP NULL
		);
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if err := ensureSQLiteRuntimeTables(ctx, db); err != nil {
		t.Fatalf("ensureSQLiteRuntimeTables: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, balance) VALUES (1, 100)`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_keys (id, key, status, quota) VALUES (1, 'sk-test', ?, 10)`, service.StatusAPIKeyActive); err != nil {
		t.Fatalf("seed api_keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (id, extra) VALUES (1, ?)`,
		`{"quota_limit":100,"quota_daily_limit":20,"quota_weekly_limit":50}`); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}

	repo := NewUsageBillingRepository(nil, db)
	cmd := &service.UsageBillingCommand{
		RequestID:           "sqlite-e2e-" + uuid.NewString(),
		APIKeyID:            1,
		UserID:              1,
		AccountID:           1,
		AccountType:         service.AccountTypeAPIKey,
		BalanceCost:         1.5,
		APIKeyQuotaCost:     1.5,
		APIKeyRateLimitCost: 1.5,
		AccountQuotaCost:    2.5,
	}

	result, err := repo.Apply(ctx, cmd)
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if result == nil || !result.Applied {
		t.Fatalf("expected applied result, got %+v", result)
	}
	if result.NewBalance == nil || *result.NewBalance != 98.5 {
		t.Fatalf("unexpected new balance: %+v", result.NewBalance)
	}

	var balance float64
	if err := db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = 1`).Scan(&balance); err != nil {
		t.Fatalf("query balance: %v", err)
	}
	if balance != 98.5 {
		t.Fatalf("balance=%v want 98.5", balance)
	}

	var quotaUsed, usage5h, usage1d, usage7d float64
	var status string
	if err := db.QueryRowContext(ctx, `
		SELECT quota_used, usage_5h, usage_1d, usage_7d, status
		FROM api_keys WHERE id = 1
	`).Scan(&quotaUsed, &usage5h, &usage1d, &usage7d, &status); err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	if quotaUsed != 1.5 || usage5h != 1.5 || usage1d != 1.5 || usage7d != 1.5 {
		t.Fatalf("unexpected api key usage: quota=%v 5h=%v 1d=%v 7d=%v", quotaUsed, usage5h, usage1d, usage7d)
	}
	if status != service.StatusAPIKeyActive {
		t.Fatalf("status=%q want %q", status, service.StatusAPIKeyActive)
	}

	var extra string
	if err := db.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id = 1`).Scan(&extra); err != nil {
		t.Fatalf("query accounts.extra: %v", err)
	}
	for _, needle := range []string{`"quota_used":2.5`, `"quota_daily_used":2.5`, `"quota_weekly_used":2.5`} {
		if !strings.Contains(extra, needle) {
			t.Fatalf("accounts.extra missing %s: %s", needle, extra)
		}
	}

	var dedupCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = ? AND api_key_id = ?`, cmd.RequestID, cmd.APIKeyID).Scan(&dedupCount); err != nil {
		t.Fatalf("query dedup: %v", err)
	}
	if dedupCount != 1 {
		t.Fatalf("dedupCount=%d want 1", dedupCount)
	}

	result2, err := repo.Apply(ctx, cmd)
	if err != nil {
		t.Fatalf("Apply duplicate error: %v", err)
	}
	if result2 == nil || result2.Applied {
		t.Fatalf("expected duplicate request to be ignored, got %+v", result2)
	}
}
