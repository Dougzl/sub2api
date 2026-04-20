//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	_ "modernc.org/sqlite"
)

func TestEnsureSQLiteRuntimeTables_NormalizesUsageLogsAndCreatesUniqueIndex(t *testing.T) {
	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (id, user_id, api_key_id, account_id, request_id, model, created_at)
		VALUES
			(1, 1, 1, 1, '', 'gpt-5.4', CURRENT_TIMESTAMP),
			(2, 1, 7, 1, 'dup-1', 'gpt-5.4', CURRENT_TIMESTAMP),
			(3, 1, 7, 1, 'dup-1', 'gpt-5.4', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed usage_logs: %v", err)
	}

	if err := ensureSQLiteRuntimeTables(ctx, db); err != nil {
		t.Fatalf("ensureSQLiteRuntimeTables: %v", err)
	}

	var normalizedEmptyCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE request_id = ''`).Scan(&normalizedEmptyCount); err != nil {
		t.Fatalf("count empty request_id: %v", err)
	}
	if normalizedEmptyCount != 0 {
		t.Fatalf("expected empty request_id values to be normalized to NULL, got %d", normalizedEmptyCount)
	}

	var dupCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE request_id = 'dup-1' AND api_key_id = 7`).Scan(&dupCount); err != nil {
		t.Fatalf("count duplicate request_id rows: %v", err)
	}
	if dupCount != 1 {
		t.Fatalf("expected duplicates to be normalized to a single request_id row, got %d", dupCount)
	}

	assertSQLiteIndexExists(t, ctx, db, "idx_usage_logs_request_id_api_key_unique")
}

func TestUsageLogRepositoryCreateBestEffort_SQLiteUniqueIndexSupportsOnConflict(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := ensureSQLiteRuntimeTables(ctx, db); err != nil {
		t.Fatalf("ensureSQLiteRuntimeTables: %v", err)
	}

	repo := newUsageLogRepositoryWithSQL(nil, db)
	log1 := &service.UsageLog{
		UserID:       1,
		APIKeyID:     99,
		AccountID:    6,
		RequestID:    "usage-log-sqlite-1",
		Model:        "gpt-5.4",
		InputTokens:  12,
		OutputTokens: 7,
		CreatedAt:    time.Now().UTC(),
	}
	accountStatsCost1 := 0.0
	log1.AccountStatsCost = &accountStatsCost1
	log2 := &service.UsageLog{
		UserID:       1,
		APIKeyID:     99,
		AccountID:    6,
		RequestID:    "usage-log-sqlite-1",
		Model:        "gpt-5.4",
		InputTokens:  12,
		OutputTokens: 7,
		CreatedAt:    time.Now().UTC(),
	}
	accountStatsCost2 := 0.0
	log2.AccountStatsCost = &accountStatsCost2

	if err := repo.CreateBestEffort(ctx, log1); err != nil {
		t.Fatalf("CreateBestEffort first insert: %v", err)
	}
	if err := repo.CreateBestEffort(ctx, log2); err != nil {
		t.Fatalf("CreateBestEffort duplicate insert: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE request_id = ? AND api_key_id = ?`, log1.RequestID, log1.APIKeyID).Scan(&count); err != nil {
		t.Fatalf("count usage_logs rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected ON CONFLICT path to keep a single row, got %d", count)
	}
}

func TestUsageLogRepositoryFlushBestEffortBatch_SQLiteFallsBackToSingleInsert(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := ensureSQLiteRuntimeTables(ctx, db); err != nil {
		t.Fatalf("ensureSQLiteRuntimeTables: %v", err)
	}

	repo := newUsageLogRepositoryWithSQL(nil, db)
	now := time.Now().UTC()
	req1 := usageLogBestEffortRequest{
		prepared: prepareUsageLogInsert(&service.UsageLog{
			UserID:           1,
			APIKeyID:         77,
			AccountID:        9,
			RequestID:        "sqlite-batch-best-effort-1",
			Model:            "gpt-5.4",
			InputTokens:      3,
			OutputTokens:     2,
			TotalCost:        0.1,
			AccountStatsCost: float64Ptr(0.1),
			CreatedAt:        now,
		}),
		apiKeyID: 77,
		resultCh: make(chan error, 1),
	}
	req2 := usageLogBestEffortRequest{
		prepared: prepareUsageLogInsert(&service.UsageLog{
			UserID:           1,
			APIKeyID:         77,
			AccountID:        9,
			RequestID:        "sqlite-batch-best-effort-1",
			Model:            "gpt-5.4",
			InputTokens:      3,
			OutputTokens:     2,
			TotalCost:        0.1,
			AccountStatsCost: float64Ptr(0.1),
			CreatedAt:        now,
		}),
		apiKeyID: 77,
		resultCh: make(chan error, 1),
	}

	repo.flushBestEffortBatch(db, []usageLogBestEffortRequest{req1, req2})

	for i, ch := range []chan error{req1.resultCh, req2.resultCh} {
		if err := <-ch; err != nil {
			t.Fatalf("result %d err = %v", i+1, err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE request_id = ? AND api_key_id = ?`, "sqlite-batch-best-effort-1", 77).Scan(&count); err != nil {
		t.Fatalf("count usage_logs rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected single row after duplicate best-effort batch insert, got %d", count)
	}
}

func openUsageLogSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:usage_log_repo_sqlite?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE usage_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			api_key_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL,
			request_id TEXT NULL,
			model TEXT NOT NULL,
			requested_model TEXT NULL,
			upstream_model TEXT NULL,
			group_id INTEGER NULL,
			subscription_id INTEGER NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_5m_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_1h_tokens INTEGER NOT NULL DEFAULT 0,
			image_output_tokens INTEGER NOT NULL DEFAULT 0,
			image_output_cost REAL NOT NULL DEFAULT 0,
			input_cost REAL NOT NULL DEFAULT 0,
			output_cost REAL NOT NULL DEFAULT 0,
			cache_creation_cost REAL NOT NULL DEFAULT 0,
			cache_read_cost REAL NOT NULL DEFAULT 0,
			total_cost REAL NOT NULL DEFAULT 0,
			actual_cost REAL NOT NULL DEFAULT 0,
			rate_multiplier REAL NOT NULL DEFAULT 1,
			account_rate_multiplier REAL NULL,
			billing_type INTEGER NOT NULL DEFAULT 0,
			request_type INTEGER NOT NULL DEFAULT 0,
			stream BOOLEAN NOT NULL DEFAULT 0,
			openai_ws_mode BOOLEAN NOT NULL DEFAULT 0,
			duration_ms INTEGER NULL,
			first_token_ms INTEGER NULL,
			user_agent TEXT NULL,
			ip_address TEXT NULL,
			image_count INTEGER NOT NULL DEFAULT 0,
			image_size TEXT NULL,
			service_tier TEXT NULL,
			reasoning_effort TEXT NULL,
			inbound_endpoint TEXT NULL,
			upstream_endpoint TEXT NULL,
			cache_ttl_overridden BOOLEAN NOT NULL DEFAULT 0,
			channel_id INTEGER NULL,
			model_mapping_chain TEXT NULL,
			billing_tier TEXT NULL,
			billing_mode TEXT NULL,
			account_stats_cost REAL NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		_ = db.Close()
		t.Fatalf("create usage_logs schema: %v", err)
	}
	return db
}

func float64Ptr(v float64) *float64 { return &v }
