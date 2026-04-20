package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageBillingRepository struct {
	db *sql.DB
}

func NewUsageBillingRepository(_ *dbent.Client, sqlDB *sql.DB) service.UsageBillingRepository {
	return &usageBillingRepository{db: sqlDB}
}

func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &service.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}

	result := &service.UsageBillingApplyResult{Applied: true}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	if isSQLiteStorage() {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
			VALUES ($1, $2, $3)
			ON CONFLICT (request_id, api_key_id) DO NOTHING
		`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected > 0 {
			var archivedFingerprint string
			err = tx.QueryRowContext(ctx, `
				SELECT request_fingerprint
				FROM usage_billing_dedup_archive
				WHERE request_id = $1 AND api_key_id = $2
			`, cmd.RequestID, cmd.APIKeyID).Scan(&archivedFingerprint)
			if err == nil {
				if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
					return false, service.ErrUsageBillingRequestConflict
				}
				return false, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			return true, nil
		}

		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, cmd.RequestID, cmd.APIKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}

	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, cmd.RequestID, cmd.APIKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, cmd.RequestID, cmd.APIKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	if cmd.SubscriptionCost > 0 && cmd.SubscriptionID != nil {
		if err := incrementUsageBillingSubscription(ctx, tx, *cmd.SubscriptionID, cmd.SubscriptionCost); err != nil {
			return err
		}
	}

	if cmd.BalanceCost > 0 {
		newBalance, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceCost)
		if err != nil {
			return err
		}
		result.NewBalance = &newBalance
	}

	if cmd.APIKeyQuotaCost > 0 {
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, cmd.APIKeyQuotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, cmd.APIKeyRateLimitCost); err != nil {
			return err
		}
	}

	if cmd.AccountQuotaCost > 0 && (strings.EqualFold(cmd.AccountType, service.AccountTypeAPIKey) || strings.EqualFold(cmd.AccountType, service.AccountTypeBedrock)) {
		quotaState, err := incrementUsageBillingAccountQuota(ctx, tx, cmd.AccountID, cmd.AccountQuotaCost)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

func incrementUsageBillingSubscription(ctx context.Context, tx *sql.Tx, subscriptionID int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions
		SET
			daily_usage_usd = daily_usage_usd + $1,
			weekly_usage_usd = weekly_usage_usd + $1,
			monthly_usage_usd = monthly_usage_usd + $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
			AND deleted_at IS NULL
			AND EXISTS (
				SELECT 1
				FROM groups g
				WHERE g.id = user_subscriptions.group_id
				  AND g.deleted_at IS NULL
			)
	`
	res, err := tx.ExecContext(ctx, updateSQL, costUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return service.ErrSubscriptionNotFound
}

func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, error) {
	if isSQLiteStorage() {
		res, err := tx.ExecContext(ctx, `
			UPDATE users
			SET balance = balance - $1,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $2 AND deleted_at IS NULL
		`, amount, userID)
		if err != nil {
			return 0, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if affected == 0 {
			return 0, service.ErrUserNotFound
		}
		var newBalance float64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&newBalance); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, service.ErrUserNotFound
			}
			return 0, err
		}
		return newBalance, nil
	}

	var newBalance float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return newBalance, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	if isSQLiteStorage() {
		res, err := tx.ExecContext(ctx, `
			UPDATE api_keys
			SET quota_used = quota_used + $1,
				status = CASE
					WHEN quota > 0
						AND status = $3
						AND quota_used < quota
						AND quota_used + $1 >= quota
					THEN $4
					ELSE status
				END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $2 AND deleted_at IS NULL
		`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected == 0 {
			return false, service.ErrAPIKeyNotFound
		}

		var quota, quotaUsed float64
		if err := tx.QueryRowContext(ctx, `
			SELECT quota, quota_used
			FROM api_keys
			WHERE id = $1 AND deleted_at IS NULL
		`, apiKeyID).Scan(&quota, &quotaUsed); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, service.ErrAPIKeyNotFound
			}
			return false, err
		}
		exhausted := quota > 0 && quotaUsed >= quota && (quotaUsed-amount) < quota
		return exhausted, nil
	}

	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	if isSQLiteStorage() {
		return incrementUsageBillingAPIKeyRateLimitSQLite(ctx, tx, apiKeyID, cost)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, cost, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func incrementUsageBillingAccountQuota(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	if isSQLiteStorage() {
		return incrementUsageBillingAccountQuotaSQLite(ctx, tx, accountID, amount)
	}
	rows, err := tx.QueryContext(ctx,
		`UPDATE accounts SET extra = (
			COALESCE(extra, '{}'::jsonb)
			|| jsonb_build_object('quota_used', COALESCE((extra->>'quota_used')::numeric, 0) + $1)
			|| CASE WHEN COALESCE((extra->>'quota_daily_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_daily_used',
					CASE WHEN `+dailyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) + $1 END,
					'quota_daily_start',
					CASE WHEN `+dailyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_daily_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+dailyExpiredExpr+` AND `+nextDailyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_daily_reset_at', `+nextDailyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
			|| CASE WHEN COALESCE((extra->>'quota_weekly_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_weekly_used',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_weekly_used')::numeric, 0) + $1 END,
					'quota_weekly_start',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_weekly_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+weeklyExpiredExpr+` AND `+nextWeeklyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_weekly_reset_at', `+nextWeeklyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
		), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0)`,
		amount, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}

func incrementUsageBillingAPIKeyRateLimitSQLite(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	var (
		usage5h, usage1d, usage7d                   float64
		window5hStart, window1dStart, window7dStart sql.NullTime
	)
	err := tx.QueryRowContext(ctx, `
		SELECT usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys
		WHERE id = $1 AND deleted_at IS NULL
	`, apiKeyID).Scan(&usage5h, &usage1d, &usage7d, &window5hStart, &window1dStart, &window7dStart)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrAPIKeyNotFound
	}
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	dayStartUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	next5hUsage, next5hStart := nextUsageWindowValue(window5hStart, usage5h, cost, 5*time.Hour, now, now)
	next1dUsage, next1dStart := nextUsageWindowValue(window1dStart, usage1d, cost, 24*time.Hour, now, dayStartUTC)
	next7dUsage, next7dStart := nextUsageWindowValue(window7dStart, usage7d, cost, 7*24*time.Hour, now, dayStartUTC)

	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys
		SET usage_5h = $1,
			usage_1d = $2,
			usage_7d = $3,
			window_5h_start = $4,
			window_1d_start = $5,
			window_7d_start = $6,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $7 AND deleted_at IS NULL
	`, next5hUsage, next1dUsage, next7dUsage, next5hStart, next1dStart, next7dStart, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func incrementUsageBillingAccountQuotaSQLite(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	extra, err := loadAccountExtraForUsageBilling(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}

	nowUTC := time.Now().UTC()
	account := &service.Account{ID: accountID, Extra: extra}

	extra["quota_used"] = account.GetQuotaUsed() + amount

	if account.GetQuotaDailyLimit() > 0 {
		if account.IsDailyQuotaPeriodExpired() {
			extra["quota_daily_used"] = amount
			extra["quota_daily_start"] = nowUTC.Format(time.RFC3339)
		} else {
			extra["quota_daily_used"] = account.GetQuotaDailyUsed() + amount
		}
	}

	if account.GetQuotaWeeklyLimit() > 0 {
		if account.IsWeeklyQuotaPeriodExpired() {
			extra["quota_weekly_used"] = amount
			extra["quota_weekly_start"] = nowUTC.Format(time.RFC3339)
		} else {
			extra["quota_weekly_used"] = account.GetQuotaWeeklyUsed() + amount
		}
	}

	service.ComputeQuotaResetAt(extra)

	payload, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET extra = $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND deleted_at IS NULL
	`, string(payload), accountID)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, service.ErrAccountNotFound
	}

	updated := &service.Account{ID: accountID, Extra: extra}
	state := &service.AccountQuotaState{
		TotalUsed:   updated.GetQuotaUsed(),
		TotalLimit:  updated.GetQuotaLimit(),
		DailyUsed:   updated.GetQuotaDailyUsed(),
		DailyLimit:  updated.GetQuotaDailyLimit(),
		WeeklyUsed:  updated.GetQuotaWeeklyUsed(),
		WeeklyLimit: updated.GetQuotaWeeklyLimit(),
	}

	if state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return state, nil
}

func loadAccountExtraForUsageBilling(ctx context.Context, tx *sql.Tx, accountID int64) (map[string]any, error) {
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT extra
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
	`, accountID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return map[string]any{}, nil
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(raw.String), &extra); err != nil {
		return nil, fmt.Errorf("decode account extra: %w", err)
	}
	if extra == nil {
		extra = map[string]any{}
	}
	return extra, nil
}

func nextUsageWindowValue(start sql.NullTime, current, amount float64, dur time.Duration, now, resetStart time.Time) (float64, time.Time) {
	if !start.Valid || start.Time.IsZero() || start.Time.Add(dur).Before(now) || start.Time.Add(dur).Equal(now) {
		return amount, resetStart
	}
	return current + amount, start.Time
}
