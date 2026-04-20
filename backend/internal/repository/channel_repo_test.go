//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// --- marshalModelMapping ---

func TestMarshalModelMapping(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]map[string]string
		wantJSON string // expected JSON output (exact match)
	}{
		{
			name:     "empty map",
			input:    map[string]map[string]string{},
			wantJSON: "{}",
		},
		{
			name:     "nil map",
			input:    nil,
			wantJSON: "{}",
		},
		{
			name: "populated map",
			input: map[string]map[string]string{
				"openai": {"gpt-4": "gpt-4-turbo"},
			},
		},
		{
			name: "nested values",
			input: map[string]map[string]string{
				"openai":    {"*": "gpt-5.4"},
				"anthropic": {"claude-old": "claude-new"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := marshalModelMapping(tt.input)
			require.NoError(t, err)

			if tt.wantJSON != "" {
				require.Equal(t, []byte(tt.wantJSON), result)
			} else {
				// round-trip: unmarshal and compare with input
				var parsed map[string]map[string]string
				require.NoError(t, json.Unmarshal(result, &parsed))
				require.Equal(t, tt.input, parsed)
			}
		})
	}
}

// --- unmarshalModelMapping ---

func TestUnmarshalModelMapping(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantNil bool
		want    map[string]map[string]string
	}{
		{
			name:    "nil data",
			input:   nil,
			wantNil: true,
		},
		{
			name:    "empty data",
			input:   []byte{},
			wantNil: true,
		},
		{
			name:    "invalid JSON",
			input:   []byte("not-json"),
			wantNil: true,
		},
		{
			name:    "type error - number",
			input:   []byte("42"),
			wantNil: true,
		},
		{
			name:    "type error - array",
			input:   []byte("[1,2,3]"),
			wantNil: true,
		},
		{
			name:  "valid JSON",
			input: []byte(`{"openai":{"gpt-4":"gpt-4-turbo"},"anthropic":{"old":"new"}}`),
			want: map[string]map[string]string{
				"openai":    {"gpt-4": "gpt-4-turbo"},
				"anthropic": {"old": "new"},
			},
		},
		{
			name:  "empty object",
			input: []byte("{}"),
			want:  map[string]map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unmarshalModelMapping(tt.input)
			if tt.wantNil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.want, result)
			}
		})
	}
}

// --- escapeLike ---

func TestEscapeLike(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no special chars",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "backslash",
			input: `a\b`,
			want:  `a\\b`,
		},
		{
			name:  "percent",
			input: "50%",
			want:  `50\%`,
		},
		{
			name:  "underscore",
			input: "a_b",
			want:  `a\_b`,
		},
		{
			name:  "all special chars",
			input: `a\b%c_d`,
			want:  `a\\b\%c\_d`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "consecutive special chars",
			input: "%_%",
			want:  `\%\_\%`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeLike(tt.input))
		})
	}
}

// --- isUniqueViolation ---

func TestIsUniqueViolation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "unique violation code 23505",
			err:  &pq.Error{Code: "23505"},
			want: true,
		},
		{
			name: "different pq error code",
			err:  &pq.Error{Code: "23503"},
			want: false,
		},
		{
			name: "non-pq error",
			err:  errors.New("some generic error"),
			want: false,
		},
		{
			name: "typed nil pq.Error",
			err: func() error {
				var pqErr *pq.Error
				return pqErr
			}(),
			want: false,
		},
		{
			name: "bare nil",
			err:  nil,
			want: false,
		},
		{
			name: "wrapped pq error with 23505",
			err:  fmt.Errorf("wrapped: %w", &pq.Error{Code: "23505"}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUniqueViolation(tt.err))
		})
	}
}

func TestChannelListOrderBy_AllowsDescendingIDSort(t *testing.T) {
	params := pagination.PaginationParams{
		SortBy:    "id",
		SortOrder: "desc",
	}

	require.Equal(t, "c.id DESC, c.id DESC", channelListOrderBy(params))
}

func TestSetGroupIDsTx_SQLiteInsertsAssociationsWithoutUnnest(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() { setRuntimeStorageEngine("") })

	db, err := sql.Open("sqlite", "file:channel_group_ids_sqlite?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE channel_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, setGroupIDsTx(ctx, db, 10, []int64{2, 3}))

	rows, err := db.QueryContext(ctx, `SELECT group_id FROM channel_groups WHERE channel_id = ? ORDER BY group_id`, 10)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var got []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		got = append(got, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []int64{2, 3}, got)
}

func TestCreateAccountStatsPricingRuleTx_SQLitePersistsJSONScopes(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() { setRuntimeStorageEngine("") })

	db, err := sql.Open("sqlite", "file:channel_account_stats_pricing_sqlite?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE channel_account_stats_pricing_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			group_ids TEXT NOT NULL DEFAULT '[]',
			account_ids TEXT NOT NULL DEFAULT '[]',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE channel_account_stats_model_pricing (
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
		CREATE TABLE channel_account_stats_pricing_intervals (
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
	`)
	require.NoError(t, err)

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	rule := &service.AccountStatsPricingRule{
		ChannelID:  9,
		Name:       "rule-1",
		GroupIDs:   []int64{10, 20},
		AccountIDs: []int64{30, 40},
		SortOrder:  1,
		Pricing:    []service.ChannelModelPricing{},
	}
	require.NoError(t, createAccountStatsPricingRuleTx(context.Background(), tx, rule))
	require.NotZero(t, rule.ID)
	require.NoError(t, tx.Commit())

	repo := &channelRepository{db: db}
	got, err := repo.loadAccountStatsPricingRules(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []int64{10, 20}, got[0].GroupIDs)
	require.Equal(t, []int64{30, 40}, got[0].AccountIDs)
}
