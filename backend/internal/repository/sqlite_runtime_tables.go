package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const sqliteRuntimeDDL = `
CREATE TABLE IF NOT EXISTS refresh_tokens (
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	token_version INTEGER NOT NULL,
	family_id TEXT NOT NULL,
	data_json TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL,
	expires_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family_id ON refresh_tokens(family_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);

CREATE TABLE IF NOT EXISTS setup_state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scheduler_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	account_id INTEGER NULL,
	group_id INTEGER NULL,
	payload TEXT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_scheduler_outbox_created_at ON scheduler_outbox(created_at);

CREATE TABLE IF NOT EXISTS usage_billing_dedup (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	request_id TEXT NOT NULL,
	api_key_id INTEGER NOT NULL,
	request_fingerprint TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_billing_dedup_request_api_key
	ON usage_billing_dedup(request_id, api_key_id);
CREATE INDEX IF NOT EXISTS idx_usage_billing_dedup_created_at
	ON usage_billing_dedup(created_at);

CREATE TABLE IF NOT EXISTS usage_billing_dedup_archive (
	request_id TEXT NOT NULL,
	api_key_id INTEGER NOT NULL,
	request_fingerprint TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL,
	archived_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (request_id, api_key_id)
);
CREATE INDEX IF NOT EXISTS idx_usage_billing_dedup_archive_created_at
	ON usage_billing_dedup_archive(created_at);

CREATE TABLE IF NOT EXISTS user_group_rate_multipliers (
	user_id INTEGER NOT NULL,
	group_id INTEGER NOT NULL,
	rate_multiplier REAL NOT NULL DEFAULT 1.0,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL,
	PRIMARY KEY (user_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_user_group_rate_multipliers_group_id
	ON user_group_rate_multipliers(group_id);

CREATE TABLE IF NOT EXISTS channels (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	description TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	model_mapping TEXT DEFAULT '{}',
	billing_model_source TEXT DEFAULT 'requested',
	restrict_models BOOLEAN NOT NULL DEFAULT 0,
	features TEXT DEFAULT '[]',
	features_config TEXT NOT NULL DEFAULT '{}',
	apply_pricing_to_account_stats BOOLEAN NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channels_status ON channels(status);

CREATE TABLE IF NOT EXISTS channel_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id INTEGER NOT NULL,
	group_id INTEGER NOT NULL UNIQUE,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_groups_channel_id ON channel_groups(channel_id);

CREATE TABLE IF NOT EXISTS channel_model_pricing (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id INTEGER NOT NULL,
	platform TEXT NOT NULL DEFAULT 'anthropic',
	models TEXT NOT NULL DEFAULT '[]',
	billing_mode TEXT NOT NULL DEFAULT 'token',
	input_price REAL NULL,
	output_price REAL NULL,
	cache_write_price REAL NULL,
	cache_read_price REAL NULL,
	image_output_price REAL NULL,
	per_request_price REAL NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_model_pricing_channel_id ON channel_model_pricing(channel_id);

CREATE TABLE IF NOT EXISTS channel_pricing_intervals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pricing_id INTEGER NOT NULL,
	min_tokens INTEGER NOT NULL DEFAULT 0,
	max_tokens INTEGER NULL,
	tier_label TEXT NULL,
	input_price REAL NULL,
	output_price REAL NULL,
	cache_write_price REAL NULL,
	cache_read_price REAL NULL,
	per_request_price REAL NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_pricing_intervals_pricing_id ON channel_pricing_intervals(pricing_id);

CREATE TABLE IF NOT EXISTS channel_account_stats_pricing_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id INTEGER NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	group_ids TEXT NOT NULL DEFAULT '[]',
	account_ids TEXT NOT NULL DEFAULT '[]',
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_account_stats_pricing_rules_channel_id
	ON channel_account_stats_pricing_rules(channel_id);

CREATE TABLE IF NOT EXISTS channel_account_stats_model_pricing (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	rule_id INTEGER NOT NULL,
	platform TEXT NOT NULL DEFAULT 'anthropic',
	models TEXT NOT NULL DEFAULT '[]',
	billing_mode TEXT NOT NULL DEFAULT 'token',
	input_price REAL NULL,
	output_price REAL NULL,
	cache_write_price REAL NULL,
	cache_read_price REAL NULL,
	image_output_price REAL NULL,
	per_request_price REAL NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_account_stats_model_pricing_rule_id
	ON channel_account_stats_model_pricing(rule_id);

CREATE TABLE IF NOT EXISTS channel_account_stats_pricing_intervals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pricing_id INTEGER NOT NULL,
	min_tokens INTEGER NOT NULL DEFAULT 0,
	max_tokens INTEGER NULL,
	tier_label TEXT NULL,
	input_price REAL NULL,
	output_price REAL NULL,
	cache_write_price REAL NULL,
	cache_read_price REAL NULL,
	per_request_price REAL NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_channel_account_stats_pricing_intervals_pricing_id
	ON channel_account_stats_pricing_intervals(pricing_id);

CREATE TABLE IF NOT EXISTS scheduled_test_plans (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id INTEGER NOT NULL,
	model_id TEXT NOT NULL DEFAULT '',
	cron_expression TEXT NOT NULL DEFAULT '*/30 * * * *',
	enabled BOOLEAN NOT NULL DEFAULT 1,
	max_results INTEGER NOT NULL DEFAULT 50,
	auto_recover BOOLEAN NOT NULL DEFAULT 0,
	last_run_at TIMESTAMP NULL,
	next_run_at TIMESTAMP NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_stp_account_id ON scheduled_test_plans(account_id);
CREATE INDEX IF NOT EXISTS idx_stp_enabled_next_run ON scheduled_test_plans(enabled, next_run_at);

CREATE TABLE IF NOT EXISTS scheduled_test_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	plan_id INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'success',
	response_text TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	latency_ms INTEGER NOT NULL DEFAULT 0,
	started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	finished_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_str_plan_created ON scheduled_test_results(plan_id, created_at DESC);
`

func ensureSQLitePragmas(ctx context.Context, db *sql.DB) error {
	for _, stmt := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlite pragma %q: %w", stmt, err)
		}
	}
	return nil
}

func ensureSQLiteRuntimeTables(ctx context.Context, db *sql.DB) error {
	for i, stmt := range strings.Split(sqliteRuntimeDDL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlite runtime table statement %d: %w", i+1, err)
		}
	}
	return ensureSQLiteCompatibilityColumns(ctx, db)
}

func ensureSQLiteCompatibilityColumns(ctx context.Context, db *sql.DB) error {
	columns := map[string]string{
		"request_type":         "INTEGER NOT NULL DEFAULT 0",
		"openai_ws_mode":       "BOOLEAN NOT NULL DEFAULT 0",
		"image_output_tokens":  "INTEGER NOT NULL DEFAULT 0",
		"image_output_cost":    "REAL NOT NULL DEFAULT 0",
		"service_tier":         "TEXT NULL",
		"reasoning_effort":     "TEXT NULL",
		"inbound_endpoint":     "TEXT NULL",
		"upstream_endpoint":    "TEXT NULL",
		"cache_ttl_overridden": "BOOLEAN NOT NULL DEFAULT 0",
		"channel_id":           "INTEGER NULL",
		"model_mapping_chain":  "TEXT NULL",
		"billing_tier":         "TEXT NULL",
		"billing_mode":         "TEXT NULL",
		"account_stats_cost":   "REAL NOT NULL DEFAULT 0",
	}
	for name, typ := range columns {
		exists, err := sqliteColumnExists(ctx, db, "usage_logs", name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.ExecContext(ctx, "ALTER TABLE usage_logs ADD COLUMN "+name+" "+typ); err != nil {
				return fmt.Errorf("sqlite add usage_logs.%s: %w", name, err)
			}
		}
	}
	return ensureSQLiteUsageLogConstraints(ctx, db)
}

func ensureSQLiteUsageLogConstraints(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return nil
	}
	hasRequestID, err := sqliteColumnExists(ctx, db, "usage_logs", "request_id")
	if err != nil {
		return err
	}
	hasAPIKeyID, err := sqliteColumnExists(ctx, db, "usage_logs", "api_key_id")
	if err != nil {
		return err
	}
	if !hasRequestID || !hasAPIKeyID {
		return nil
	}
	if _, err := db.ExecContext(ctx, `UPDATE usage_logs SET request_id = NULL WHERE request_id = ''`); err != nil {
		return fmt.Errorf("sqlite normalize usage_logs.request_id empty string: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
WITH ranked AS (
	SELECT
		id,
		ROW_NUMBER() OVER (PARTITION BY api_key_id, request_id ORDER BY id) AS rn
	FROM usage_logs
	WHERE request_id IS NOT NULL
)
UPDATE usage_logs
SET request_id = NULL
WHERE id IN (SELECT id FROM ranked WHERE rn > 1)
`); err != nil {
		return fmt.Errorf("sqlite normalize duplicate usage_logs request ids: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_logs_request_id_api_key_unique
	ON usage_logs (request_id, api_key_id)
`); err != nil {
		return fmt.Errorf("sqlite create usage_logs request_id/api_key unique index: %w", err)
	}
	return nil
}

func sqliteColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}
