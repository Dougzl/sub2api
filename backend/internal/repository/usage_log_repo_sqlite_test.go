//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
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

func TestUsageLogRepositoryGetAccountUsageStats_SQLiteUsesStrftime(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, total_cost, actual_cost, account_stats_cost, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, 1, 9, 10, "stats-1", "gpt-5.4", 12, 8, 1.5, 1.25, 1.25, start.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed usage_logs: %v", err)
	}

	resp, err := repo.GetAccountUsageStats(ctx, 10, start, end)
	if err != nil {
		t.Fatalf("GetAccountUsageStats: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(resp.History) != 1 {
		t.Fatalf("expected 1 history item, got %d", len(resp.History))
	}
	if resp.History[0].Date != "2026-04-20" {
		t.Fatalf("expected history date 2026-04-20, got %q", resp.History[0].Date)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("expected 1 model stat, got %d", len(resp.Models))
	}
	if resp.Models[0].Model != "gpt-5.4" {
		t.Fatalf("expected model gpt-5.4, got %q", resp.Models[0].Model)
	}
	if resp.Models[0].Requests != 1 {
		t.Fatalf("expected model requests=1, got %d", resp.Models[0].Requests)
	}
}

func openUsageLogSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:usage_log_repo_sqlite?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NULL,
			username TEXT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL
		);

		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NULL,
			status INTEGER NOT NULL DEFAULT 1,
			deleted_at TIMESTAMP NULL
		);

		CREATE TABLE accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			status INTEGER NOT NULL DEFAULT 1,
			schedulable BOOLEAN NOT NULL DEFAULT 1,
			rate_limited_at TIMESTAMP NULL,
			rate_limit_reset_at TIMESTAMP NULL,
			overload_until TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL
		);

		CREATE TABLE groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NULL
		);

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

func TestUsageLogRepositoryGetDashboardStats_SQLiteIncludesUsageMetrics(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	now := time.Now().UTC()
	todayStart := timezone.Today()

	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, username, created_at) VALUES (1, 'u1@example.com', 'u1', ?)`, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_keys (id, user_id, name, status) VALUES (10, 1, 'k1', ?)`, service.StatusActive); err != nil {
		t.Fatalf("seed api_keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (id, status, schedulable) VALUES (20, ?, 1)`, service.StatusActive); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, account_stats_cost, account_rate_multiplier,
			duration_ms, created_at
		) VALUES (1, 10, 20, 'dash-1', 'gpt-5.4', 100, 50, 10, 5, 1.5, 1.2, 1.2, 1, 600, ?)
	`, todayStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed usage_log dash-1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, account_stats_cost, account_rate_multiplier,
			duration_ms, created_at
		) VALUES (1, 10, 20, 'dash-2', 'gpt-5.4', 20, 30, 0, 0, 0.5, 0.4, 0.4, 1, 400, ?)
	`, todayStart.Add(3*time.Hour)); err != nil {
		t.Fatalf("seed usage_log dash-2: %v", err)
	}

	stats, err := repo.GetDashboardStats(ctx)
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.TotalRequests != 2 {
		t.Fatalf("expected total_requests=2, got %d", stats.TotalRequests)
	}
	if stats.TodayRequests != 2 {
		t.Fatalf("expected today_requests=2, got %d", stats.TodayRequests)
	}
	if stats.TotalTokens != 215 {
		t.Fatalf("expected total_tokens=215, got %d", stats.TotalTokens)
	}
	if stats.TodayTokens != 215 {
		t.Fatalf("expected today_tokens=215, got %d", stats.TodayTokens)
	}
	if stats.ActiveUsers != 1 {
		t.Fatalf("expected active_users=1, got %d", stats.ActiveUsers)
	}
	if stats.AverageDurationMs != 500 {
		t.Fatalf("expected average_duration_ms=500, got %v", stats.AverageDurationMs)
	}
}

func TestUsageLogRepositoryGetUsageTrendWithFilters_SQLiteReturnsBuckets(t *testing.T) {
	if err := timezone.Init("Asia/Shanghai"); err != nil {
		t.Fatalf("timezone.Init: %v", err)
	}
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'trend-1', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.08, ?),
			(1, 10, 20, 'trend-2', 'gpt-5.4', 20, 15, 5, 0, 0.2, 0.15, ?)
	`, start.Add(1*time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed usage trend data: %v", err)
	}

	trend, err := repo.GetUsageTrendWithFilters(timezone.WithUserLocation(ctx, "Asia/Shanghai"), start, end, "hour", 0, 0, 0, 0, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("GetUsageTrendWithFilters: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 trend buckets, got %d", len(trend))
	}
	if trend[0].Date != "2026-04-20 09:00" {
		t.Fatalf("expected first bucket date=2026-04-20 09:00, got %q", trend[0].Date)
	}
	if trend[0].TotalTokens != 15 {
		t.Fatalf("expected first bucket total_tokens=15, got %d", trend[0].TotalTokens)
	}
	if trend[1].Date != "2026-04-20 10:00" {
		t.Fatalf("expected second bucket date=2026-04-20 10:00, got %q", trend[1].Date)
	}
	if trend[1].TotalTokens != 40 {
		t.Fatalf("expected second bucket total_tokens=40, got %d", trend[1].TotalTokens)
	}
}

func TestUsageLogRepositoryGetUserUsageTrend_SQLiteReturnsTopUsers(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, username, created_at) VALUES
			(1, 'u1@example.com', 'u1', ?),
			(2, 'u2@example.com', 'u2', ?);
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'u-trend-1', 'gpt-5.4', 40, 20, 0, 0, 0.4, 0.35, ?),
			(2, 11, 21, 'u-trend-2', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.09, ?)
	`, start, start, start.Add(1*time.Hour), start.Add(1*time.Hour)); err != nil {
		t.Fatalf("seed user trend data: %v", err)
	}

	trend, err := repo.GetUserUsageTrend(timezone.WithUserLocation(ctx, "Asia/Shanghai"), start, end, "day", 12)
	if err != nil {
		t.Fatalf("GetUserUsageTrend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 user trend rows, got %d", len(trend))
	}
	if trend[0].Username != "u1" {
		t.Fatalf("expected first username u1, got %q", trend[0].Username)
	}
	if trend[0].Tokens != 60 {
		t.Fatalf("expected first user tokens=60, got %d", trend[0].Tokens)
	}
}

func TestUsageLogRepositoryGetAPIKeyUsageTrend_SQLiteReturnsTopKeys(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO api_keys (id, user_id, name, status) VALUES
			(10, 1, 'k1', ?),
			(11, 2, 'k2', ?)
	`, service.StatusActive, service.StatusActive); err != nil {
		t.Fatalf("seed api keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'k-trend-1', 'gpt-5.4', 40, 20, 0, 0, 0.4, 0.35, ?),
			(2, 11, 21, 'k-trend-2', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.09, ?)
	`, start.Add(1*time.Hour), start.Add(1*time.Hour)); err != nil {
		t.Fatalf("seed api key trend data: %v", err)
	}

	trend, err := repo.GetAPIKeyUsageTrend(timezone.WithUserLocation(ctx, "Asia/Shanghai"), start, end, "day", 12)
	if err != nil {
		t.Fatalf("GetAPIKeyUsageTrend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 api key trend rows, got %d", len(trend))
	}
	if trend[0].KeyName != "k1" {
		t.Fatalf("expected first key name k1, got %q", trend[0].KeyName)
	}
	if trend[0].Tokens != 60 {
		t.Fatalf("expected first key tokens=60, got %d", trend[0].Tokens)
	}
}

func TestUsageLogRepositoryGetGroupStatsWithFilters_SQLiteReturnsGroups(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO groups (id, name) VALUES (7, 'default'), (8, 'vip')
	`); err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model, group_id,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, account_stats_cost, account_rate_multiplier, created_at
		) VALUES
			(1, 10, 20, 'g-stats-1', 'gpt-5.4', 7, 30, 10, 0, 0, 0.4, 0.3, 0.3, 1, ?),
			(2, 11, 21, 'g-stats-2', 'gpt-5.4', 8, 30, 20, 0, 0, 0.6, 0.5, 0.5, 1, ?)
	`, start.Add(1*time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed group stats data: %v", err)
	}

	stats, err := repo.GetGroupStatsWithFilters(ctx, start, end, 0, 0, 0, 0, nil, nil, nil)
	if err != nil {
		t.Fatalf("GetGroupStatsWithFilters: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 group rows, got %d", len(stats))
	}
	if stats[0].GroupName != "vip" {
		t.Fatalf("expected top group vip, got %q", stats[0].GroupName)
	}
	if stats[0].TotalTokens != 50 {
		t.Fatalf("expected top group total_tokens=50, got %d", stats[0].TotalTokens)
	}
}

func TestUsageLogRepositoryGetUserUsageTrendByUserID_SQLiteReturnsTrend(t *testing.T) {
	if err := timezone.Init("Asia/Shanghai"); err != nil {
		t.Fatalf("timezone.Init: %v", err)
	}
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'self-trend-1', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.08, ?),
			(1, 10, 20, 'self-trend-2', 'gpt-5.4', 20, 15, 0, 0, 0.2, 0.16, ?)
	`, start.Add(1*time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed self trend data: %v", err)
	}

	trend, err := repo.GetUserUsageTrendByUserID(timezone.WithUserLocation(ctx, "Asia/Shanghai"), 1, start, end, "hour")
	if err != nil {
		t.Fatalf("GetUserUsageTrendByUserID: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 self trend buckets, got %d", len(trend))
	}
	if trend[0].Date != "2026-04-20 09:00" {
		t.Fatalf("expected first self bucket date=2026-04-20 09:00, got %q", trend[0].Date)
	}
	if trend[0].TotalTokens != 15 {
		t.Fatalf("expected first self bucket total_tokens=15, got %d", trend[0].TotalTokens)
	}
}

func TestUsageLogRepositoryGetDailyStatsAggregated_SQLiteReturnsDates(t *testing.T) {
	if err := timezone.Init("Asia/Shanghai"); err != nil {
		t.Fatalf("timezone.Init: %v", err)
	}
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'daily-1', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.08, ?),
			(1, 10, 20, 'daily-2', 'gpt-5.4', 20, 10, 0, 0, 0.2, 0.16, ?)
	`, start.Add(1*time.Hour), start.Add(25*time.Hour)); err != nil {
		t.Fatalf("seed daily stats data: %v", err)
	}

	rows, err := repo.GetDailyStatsAggregated(timezone.WithUserLocation(ctx, "Asia/Shanghai"), 1, start, end)
	if err != nil {
		t.Fatalf("GetDailyStatsAggregated: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 daily rows, got %d", len(rows))
	}
	if rows[0]["date"] != "2026-04-20" {
		t.Fatalf("expected first date 2026-04-20, got %#v", rows[0]["date"])
	}
}

func TestSQLiteUsageBuckets_RespectUserTimezoneBoundary(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	baseCtx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)

	if _, err := db.ExecContext(baseCtx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'tz-boundary-1', 'gpt-5.4', 10, 5, 0.1, 0.08, ?)
	`, time.Date(2026, 4, 20, 18, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed tz boundary data: %v", err)
	}

	nyTrend, err := repo.GetUserUsageTrendByUserID(timezone.WithUserLocation(baseCtx, "America/New_York"), 1, start, end, "hour")
	if err != nil {
		t.Fatalf("GetUserUsageTrendByUserID New York: %v", err)
	}
	if len(nyTrend) != 1 || nyTrend[0].Date != "2026-04-20 14:00" {
		t.Fatalf("expected New York hour bucket 2026-04-20 14:00, got %#v", nyTrend)
	}

	shTrend, err := repo.GetUserUsageTrendByUserID(timezone.WithUserLocation(baseCtx, "Asia/Shanghai"), 1, start, end, "hour")
	if err != nil {
		t.Fatalf("GetUserUsageTrendByUserID Shanghai: %v", err)
	}
	if len(shTrend) != 1 || shTrend[0].Date != "2026-04-21 02:00" {
		t.Fatalf("expected Shanghai hour bucket 2026-04-21 02:00, got %#v", shTrend)
	}

	nyDaily, err := repo.GetDailyStatsAggregated(timezone.WithUserLocation(baseCtx, "America/New_York"), 1, start, end)
	if err != nil {
		t.Fatalf("GetDailyStatsAggregated New York: %v", err)
	}
	if len(nyDaily) != 1 || nyDaily[0]["date"] != "2026-04-20" {
		t.Fatalf("expected New York day bucket 2026-04-20, got %#v", nyDaily)
	}

	shDaily, err := repo.GetDailyStatsAggregated(timezone.WithUserLocation(baseCtx, "Asia/Shanghai"), 1, start, end)
	if err != nil {
		t.Fatalf("GetDailyStatsAggregated Shanghai: %v", err)
	}
	if len(shDaily) != 1 || shDaily[0]["date"] != "2026-04-21" {
		t.Fatalf("expected Shanghai day bucket 2026-04-21, got %#v", shDaily)
	}
}

func TestSQLiteUsageBuckets_RespectDSTOffsetAtEventTime(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	baseCtx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)

	if _, err := db.ExecContext(baseCtx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'dst-winter', 'gpt-5.4', 10, 5, 0.1, 0.08, ?),
			(1, 10, 20, 'dst-summer', 'gpt-5.4', 10, 5, 0.1, 0.08, ?)
	`, time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC), time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed dst data: %v", err)
	}

	trend, err := repo.GetUserUsageTrendByUserID(timezone.WithUserLocation(baseCtx, "America/New_York"), 1, start, end, "hour")
	if err != nil {
		t.Fatalf("GetUserUsageTrendByUserID dst: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 DST buckets, got %#v", trend)
	}
	if trend[0].Date != "2026-01-15 07:00" {
		t.Fatalf("expected winter bucket 2026-01-15 07:00, got %q", trend[0].Date)
	}
	if trend[1].Date != "2026-07-15 08:00" {
		t.Fatalf("expected summer bucket 2026-07-15 08:00, got %q", trend[1].Date)
	}
}

func TestUsageLogRepositoryGetStatsWithFilters_SQLiteReturnsEndpointPaths(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, inbound_endpoint, upstream_endpoint, created_at
		) VALUES
			(1, 10, 20, 'ep-1', 'gpt-5.4', 10, 5, 0, 0, 0.1, 0.08, '/v1/chat/completions', '/chat', ?)
	`, start.Add(1*time.Hour)); err != nil {
		t.Fatalf("seed endpoint path data: %v", err)
	}

	stats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{
		UserID:    1,
		StartTime: &start,
		EndTime:   &end,
	})
	if err != nil {
		t.Fatalf("GetStatsWithFilters: %v", err)
	}
	if len(stats.EndpointPaths) != 1 {
		t.Fatalf("expected 1 endpoint path row, got %d", len(stats.EndpointPaths))
	}
	if stats.EndpointPaths[0].Endpoint != "/v1/chat/completions -> /chat" {
		t.Fatalf("unexpected endpoint path: %q", stats.EndpointPaths[0].Endpoint)
	}
}

func TestUsageLogRepositoryGetGeminiUsageTotalsBatch_SQLiteReturnsTotals(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	db := openUsageLogSQLiteDB(t)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, request_id, model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			total_cost, actual_cost, created_at
		) VALUES
			(1, 10, 20, 'gm-1', 'gemini-2.5-flash', 10, 5, 0, 0, 0.1, 0.08, ?),
			(1, 10, 20, 'gm-2', 'gemini-2.5-pro', 20, 10, 0, 0, 0.2, 0.16, ?)
	`, start.Add(1*time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed gemini totals data: %v", err)
	}

	totals, err := repo.GetGeminiUsageTotalsBatch(ctx, []int64{20}, start, end)
	if err != nil {
		t.Fatalf("GetGeminiUsageTotalsBatch: %v", err)
	}
	got, ok := totals[20]
	if !ok {
		t.Fatal("expected account 20 totals")
	}
	if got.FlashRequests != 1 || got.ProRequests != 1 {
		t.Fatalf("unexpected request split: flash=%d pro=%d", got.FlashRequests, got.ProRequests)
	}
	if got.FlashTokens != 15 || got.ProTokens != 30 {
		t.Fatalf("unexpected token split: flash=%d pro=%d", got.FlashTokens, got.ProTokens)
	}
}
