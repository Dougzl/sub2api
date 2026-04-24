//go:build unit

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newGroupSQLiteTestClient(t *testing.T) (*dbent.Client, *sql.DB) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name)

	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client, db
}

func TestGroupRepositoryDeleteCascade_SQLiteDoesNotUseForUpdate(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	client, db := newGroupSQLiteTestClient(t)
	ctx := context.Background()
	created, err := client.Group.Create().
		SetName("sqlite-delete-group").
		SetPlatform(service.PlatformAnthropic).
		SetRateMultiplier(1).
		SetSortOrder(1).
		SetIsExclusive(false).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetDefaultValidityDays(0).
		SetClaudeCodeOnly(false).
		Save(ctx)
	require.NoError(t, err)

	repo := newGroupRepositoryWithSQL(client, db)
	affectedUsers, err := repo.DeleteCascade(ctx, created.ID)
	require.NoError(t, err)
	require.Empty(t, affectedUsers)

	_, err = repo.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, service.ErrGroupNotFound)
}

func TestGroupRepositoryGetAccountCount_SQLite(t *testing.T) {
	setRuntimeStorageEngine("sqlite")
	t.Cleanup(func() {
		setRuntimeStorageEngine("")
	})

	client, db := newGroupSQLiteTestClient(t)
	ctx := context.Background()

	groupEntity, err := client.Group.Create().
		SetName("sqlite-count-group").
		SetPlatform(service.PlatformAnthropic).
		SetRateMultiplier(1).
		SetSortOrder(1).
		SetIsExclusive(false).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetDefaultValidityDays(0).
		SetClaudeCodeOnly(false).
		Save(ctx)
	require.NoError(t, err)

	accountEntity, err := client.Account.Create().
		SetName("sqlite-count-account").
		SetPlatform(service.PlatformAnthropic).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetCredentials(map[string]any{}).
		SetExtra(map[string]any{}).
		SetConcurrency(1).
		SetPriority(1).
		SetSchedulable(true).
		SetRateLimitResetAt(time.Now().UTC().Add(5 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES (?, ?, 1, CURRENT_TIMESTAMP)`, accountEntity.ID, groupEntity.ID)
	require.NoError(t, err)

	repo := newGroupRepositoryWithSQL(client, db)
	total, active, err := repo.GetAccountCount(ctx, groupEntity.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(1), active)
}
