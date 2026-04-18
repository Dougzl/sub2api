// Package repository 提供应用程序的基础设施层组件。
// 包括数据库连接初始化、ORM 客户端管理、Redis 连接、数据库迁移等核心功能。
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/migrations"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq" // PostgreSQL 驱动，通过副作用导入注册驱动
	_ "modernc.org/sqlite"
)

// InitEnt 初始化 Ent ORM 客户端并返回客户端实例和底层的 *sql.DB。
//
// 该函数执行以下操作：
//  1. 初始化全局时区设置，确保时间处理一致性
//  2. 建立 PostgreSQL 数据库连接
//  3. 自动执行数据库迁移，确保 schema 与代码同步
//  4. 创建并返回 Ent 客户端实例
//
// 重要提示：调用者必须负责关闭返回的 ent.Client（关闭时会自动关闭底层的 driver/db）。
//
// 参数：
//   - cfg: 应用程序配置，包含数据库连接信息和时区设置
//
// 返回：
//   - *ent.Client: Ent ORM 客户端，用于执行数据库操作
//   - *sql.DB: 底层的 SQL 数据库连接，可用于直接执行原生 SQL
//   - error: 初始化过程中的错误
func InitEnt(cfg *config.Config) (*ent.Client, *sql.DB, error) {
	setRuntimeStorageEngine(cfg.Database.Engine)
	// 优先初始化时区设置，确保所有时间操作使用统一的时区。
	// 这对于跨时区部署和日志时间戳的一致性至关重要。
	if err := timezone.Init(cfg.Timezone); err != nil {
		return nil, nil, err
	}

	dialectName := dialect.Postgres
	dsn := cfg.Database.DSNWithTimezone(cfg.Timezone)
	if stringsEqualFold(cfg.Database.Engine, "sqlite") {
		dialectName = dialect.SQLite
		dsn = sqliteDSN(cfg.Database.DBName)
	}
	var drv *entsql.Driver
	var err error
	if stringsEqualFold(cfg.Database.Engine, "sqlite") {
		sqlDB, openErr := sql.Open("sqlite", dsn)
		if openErr != nil {
			return nil, nil, openErr
		}
		drv = entsql.OpenDB(dialectName, sqlDB)
	} else {
		drv, err = entsql.Open(dialectName, dsn)
	}
	if err != nil {
		return nil, nil, err
	}
	applyDBPoolSettings(drv.DB(), cfg)

	migrationCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 创建 Ent 客户端，绑定到已配置的数据库驱动。
	client := ent.NewClient(ent.Driver(drv))
	if stringsEqualFold(cfg.Database.Engine, "sqlite") {
		if err := ensureSQLitePragmas(migrationCtx, drv.DB()); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		if err := client.Schema.Create(migrationCtx); err != nil {
			_ = client.Close()
			return nil, nil, fmt.Errorf("create sqlite schema: %w", err)
		}
		if err := ensureSQLiteRuntimeTables(migrationCtx, drv.DB()); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
	} else {
		if err := applyMigrationsFS(migrationCtx, drv.DB(), migrations.FS); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
	}

	// 启动阶段：从配置或数据库中确保系统密钥可用。
	if err := ensureBootstrapSecrets(migrationCtx, client, cfg); err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	// 在密钥补齐后执行完整配置校验，避免空 jwt.secret 导致服务运行时失败。
	if err := cfg.Validate(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("validate config after secret bootstrap: %w", err)
	}

	// SIMPLE 模式：启动时补齐各平台默认分组。
	// - anthropic/openai/gemini: 确保存在 <platform>-default
	// - antigravity: 仅要求存在 >=2 个未软删除分组（用于 claude/gemini 混合调度场景）
	if cfg.RunMode == config.RunModeSimple {
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer seedCancel()
		if err := ensureSimpleModeDefaultGroups(seedCtx, client); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
		if err := ensureSimpleModeAdminConcurrency(seedCtx, client); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
	}

	return client, drv.DB(), nil
}

func sqliteDSN(path string) string {
	if path == "" {
		path = "./data/sub2api.db"
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
}

func stringsEqualFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), b)
}
