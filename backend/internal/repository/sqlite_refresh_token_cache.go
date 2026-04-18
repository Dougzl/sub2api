package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sqliteRefreshTokenCache struct {
	db *sql.DB
}

func NewSQLiteRefreshTokenCache(db *sql.DB) service.RefreshTokenCache {
	return &sqliteRefreshTokenCache{db: db}
}

func (c *sqliteRefreshTokenCache) StoreRefreshToken(ctx context.Context, tokenHash string, data *service.RefreshTokenData, ttl time.Duration) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("sqlite refresh token cache not configured")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	expiresAt := data.ExpiresAt
	if expiresAt.IsZero() && ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	_, err = c.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO refresh_tokens
			(token_hash, user_id, token_version, family_id, data_json, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, tokenHash, data.UserID, data.TokenVersion, data.FamilyID, string(raw), data.CreatedAt, expiresAt)
	return err
}

func (c *sqliteRefreshTokenCache) GetRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("sqlite refresh token cache not configured")
	}
	var raw string
	err := c.db.QueryRowContext(ctx, `SELECT data_json FROM refresh_tokens WHERE token_hash = ?`, tokenHash).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrRefreshTokenNotFound
		}
		return nil, err
	}
	var data service.RefreshTokenData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}
	if !data.ExpiresAt.IsZero() && !data.ExpiresAt.After(time.Now()) {
		_ = c.DeleteRefreshToken(ctx, tokenHash)
		return nil, service.ErrRefreshTokenNotFound
	}
	return &data, nil
}

func (c *sqliteRefreshTokenCache) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	if c == nil || c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE token_hash = ?`, tokenHash)
	return err
}

func (c *sqliteRefreshTokenCache) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	if c == nil || c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, userID)
	return err
}

func (c *sqliteRefreshTokenCache) DeleteTokenFamily(ctx context.Context, familyID string) error {
	if c == nil || c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE family_id = ?`, familyID)
	return err
}

func (c *sqliteRefreshTokenCache) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *sqliteRefreshTokenCache) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func (c *sqliteRefreshTokenCache) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	return c.listTokenHashes(ctx, `SELECT token_hash FROM refresh_tokens WHERE user_id = ?`, userID)
}

func (c *sqliteRefreshTokenCache) GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error) {
	return c.listTokenHashes(ctx, `SELECT token_hash FROM refresh_tokens WHERE family_id = ?`, familyID)
}

func (c *sqliteRefreshTokenCache) IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error) {
	if c == nil || c.db == nil {
		return false, nil
	}
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM refresh_tokens WHERE family_id = ? AND token_hash = ?`, familyID, tokenHash).Scan(&count)
	return count > 0, err
}

func (c *sqliteRefreshTokenCache) listTokenHashes(ctx context.Context, query string, arg any) ([]string, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tokenHash string
		if err := rows.Scan(&tokenHash); err != nil {
			return nil, err
		}
		out = append(out, tokenHash)
	}
	return out, rows.Err()
}
