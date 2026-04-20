# brainstorm: migrate postgres redis to sqlite

## Goal

Replace the project's current PostgreSQL + Redis runtime storage dependencies with an SQLite-based storage design, so deployment no longer requires separate PostgreSQL and Redis services.

## What I already know

* User request is now normalized to: "将工程中 postgres 数据和 redis 全部改成 sqlite 数据库".
* Project is a Go monorepo backend + frontend. Backend uses Ent ORM and raw SQL.
* Current SQL database is PostgreSQL: `backend/internal/repository/ent.go` imports `github.com/lib/pq`, opens `dialect.Postgres`, and runs SQL migrations from `backend/migrations`.
* Current cache/coordination layer is Redis: `backend/internal/repository/wire.go` provides `*redis.Client`; generated DI wires many cache implementations to Redis.
* Deploy files currently start app + PostgreSQL + Redis: `deploy/docker-compose*.yml`, `.env.example`, docs, install scripts.
* The repo already depends on `modernc.org/sqlite` and uses it as the embedded runtime database dependency.
* The project now consistently targets embedded SQLite as the single-database direction.

## Assumptions (temporary)

* The desired database is SQLite, not HTTP/2 (`h2`) support.
* The desired result is a single-database SQLite deployment without PostgreSQL/Redis containers.
* Production-grade parity is expected unless user narrows scope.

## Open Questions

* MVP storage target: SQLite-only implementation and single-binary deployment.

## Requirements (evolving)

* Remove PostgreSQL runtime requirement.
* Remove Redis runtime requirement.
* Preserve existing application behavior where feasible.
* Update deployment/config/docs/tests to match the new storage model.

## Acceptance Criteria (evolving)

* [ ] Backend starts without PostgreSQL and Redis services.
* [ ] Schema migrations initialize the replacement database from a clean state.
* [ ] Existing DB-backed core flows still pass tests or smoke checks.
* [ ] Redis-backed cache/lock/rate-limit/session semantics are replaced or safely degraded.
* [ ] Deploy examples no longer require PostgreSQL/Redis.

## Definition of Done (team quality bar)

* Tests added/updated where appropriate.
* Lint / typecheck / CI-relevant checks green.
* Docs/config examples updated.
* Rollback / migration limitations documented.

## Out of Scope (explicit)

* Data migration from existing production PostgreSQL/Redis to SQLite until scope is confirmed.
* Multi-node distributed Redis semantics unless user explicitly requires clustered deployment.

## Research Notes

### What SQLite provides

* Official SQLite docs describe SQLite as an embedded SQL database with a single-file deployment model and broad language/runtime support.
* SQLite does not provide PostgreSQL feature parity, so PostgreSQL-specific SQL still requires compatibility refactors.
* SQLite is file-based and embedded, which aligns with the project's single-binary deployment goal.

### Constraints from this repo

* Backend is Go, and SQLite is a natural embedded fit via `modernc.org/sqlite`.
* Current Ent setup uses PostgreSQL dialect and `lib/pq` DSN.
* Migrations and repository raw SQL use many PostgreSQL features: `BIGSERIAL`, `JSONB`, `BIGINT[]`, `timestamptz`, `ON CONFLICT`, `RETURNING`, `FOR UPDATE SKIP LOCKED`, `CREATE INDEX CONCURRENTLY`, `pg_*` catalogs/extensions/partitioning.
* Redis is used beyond simple cache: distributed locks, rate limiting, active sessions, scheduler snapshots, pub/sub invalidation, Redis TIME, Lua scripts, sorted sets, TTL, counters, queues, health/ops metrics.

### Feasible approaches here

**Approach A: Strict SQLite-only mode**

* How it works: use the existing `modernc.org/sqlite` dependency and Ent SQLite dialect; rewrite migrations and PostgreSQL-specific raw SQL; replace Redis features with SQLite tables and in-process coordination where feasible.
* Pros: matches the current SQLite runtime direction.
* Cons: still requires SQL compatibility work and careful handling of Redis semantics, but is much more practical than a Java-sidecar database.

**Approach B: Embedded SQLite single-store**

* How it works: use existing `modernc.org/sqlite` dependency and Ent SQLite dialect; rewrite PostgreSQL-specific migrations/raw SQL to SQLite-compatible SQL; replace Redis with SQLite/in-process cache tables where needed.
* Pros: best fit for Go single-binary/no external service; existing dependency; simpler deploy.
* Cons: SQLite-specific; concurrency/performance/SQL differences still require care.

**Approach C: Transitional storage abstraction**

* How it works: keep PostgreSQL/Redis as default; introduce pluggable DB/cache interfaces with a SQLite experimental backend behind config; migrate feature slices incrementally.
* Pros: lowest operational risk and easier testability.
* Cons: does not immediately remove PostgreSQL/Redis everywhere.

## Technical Notes

* Inspected: `backend/go.mod`, `backend/internal/config/config.go`, `backend/internal/repository/ent.go`, `backend/internal/repository/wire.go`, `backend/migrations/`, `deploy/docker-compose*.yml`, `deploy/.env.example`.
* Official SQLite docs: https://www.sqlite.org/docs.html.
* Go SQL driver list: https://go.dev/wiki/SQLDrivers.

## Decision (ADR-lite)

**Context**: The implementation direction is now SQLite-only. The project currently depends on PostgreSQL for durable SQL data and Redis for cache/lock/rate-limit/session/scheduler/ops runtime state.

**Decision**: Replace both PostgreSQL and Redis runtime dependencies with SQLite-backed storage. Prefer embedded SQLite and explicit compatibility refactors rather than trying to preserve PostgreSQL semantics via a compatibility layer.

**Consequences**:
* PostgreSQL containers/config/docs must be replaced by SQLite config/docs and embedded-file deployment defaults.
* Redis containers/config/docs must be removed; Redis semantics need SQLite-backed replacements or explicitly safe degradation.
* SQL migrations and raw SQL using PostgreSQL-only features need compatibility fixes.
* Multi-node distributed semantics previously provided by Redis require SQL locks/tables or are out of MVP unless explicitly retained.

## MVP Scope Decision

User selected option 3: first make the backend start and core flows work without PostgreSQL/Redis. Complex Redis parity (distributed locks, pub/sub, scheduler cache, ops leader election) can be completed incrementally after the SQLite-only baseline is running.

## Implementation Notes (2026-04-18)

* Added `database.engine` with default `sqlite` and SQLite runtime config.
* Default database endpoint now targets a local SQLite database file.
* Redis is disabled by default with `redis.enabled=false`; `ProvideRedis` returns nil when disabled.
* SQLite path skips PostgreSQL SQL migrations and uses Ent schema creation for clean SQLite databases because existing migrations contain PostgreSQL-only DDL.
* Added SQLite runtime table for refresh tokens and a DB-backed `RefreshTokenCache` provider for SQLite core auth flows.
* Added nil-safe/no-op fallbacks for core cache paths where Redis is disabled: rate limiter, API key cache, email cache, billing cache, concurrency cache, scheduler cache, RPM/session limit cache, error passthrough cache, TLS fingerprint profile cache.
* Updated `deploy/config.example.yaml` to document SQLite-only defaults.

## Known MVP Limitations

* Full PostgreSQL migration history is not yet translated to SQLite SQL; clean SQLite databases are initialized from Ent schema.
* Redis features are degraded for MVP when `redis.enabled=false`; multi-node consistency, pub/sub invalidation, sorted-set scheduling snapshots, Redis TIME, Lua scripts, and distributed leader locks are not fully SQLite-backed yet.
* Local environment currently lacks Go tooling (`go`/`gofmt` not on PATH), so compile/test validation must be run in an environment with Go installed.

## Follow-up Remediation (developer pass)

Addressed evaluator P0 findings:

* Added Redis-disabled nil-safe/no-op fallbacks for remaining runtime cache implementations:
  * redeem, totp, gemini token, temp unsched, timeout counter, internal500 counter, proxy latency, dashboard, update, identity, user message queue.
* Guarded `NewSessionLimitCache` Lua preload when `rdb == nil` and made `ProvideSessionLimitCache` safe for Redis-disabled mode.
* Changed Ops service providers to only call `Start()` when `cfg.Ops.Enabled` is true, reducing nil Redis/background worker risk in the default SQLite-only MVP.
* Made SQLite runtime refresh-token DDL more portable by using `TIMESTAMP WITH TIME ZONE` and executing each DDL statement separately.
* Improved no-op concurrency load output to carry account/user IDs.

Validation performed:

* `git diff --check` passes.
* Basic brace-balance script passes for newly touched files.

Still blocked:

* Go toolchain is not installed on this machine (`go`/`gofmt` not found), so compile tests and formatting still need to be run elsewhere.

## Additional Remediation (second developer pass)

* Removed the SQLite-incompatible upsert assumption from the SQLite refresh token cache. Store now uses a transaction with delete-then-insert, which is easier to validate over the SQLite path.
* Added SQLite schema bootstrap compatibility handling before Ent schema creation:
  * `JSONB` domain alias to `JSON`
  * `TIMESTAMPTZ` domain alias to `TIMESTAMP WITH TIME ZONE`
* Kept SQLite runtime DDL statement-by-statement execution.
* Added SQLite-safe `DBDumper` fallback: backup/restore now returns an explicit unsupported error in SQLite mode instead of invoking `pg_dump`/`psql`.

Validation performed:

* `git diff --check` passes after these changes.
* Brace-balance script passes for newly touched SQLite/backup/session files.

Remaining hard requirement:

* Still must run `gofmt`, `go test ./...`, and an actual SQLite startup smoke test in an environment with Go installed.

## Core SQL Static Hardening (Option A pass)

Focused on startup/auth/settings/billing/account hot-path SQL that used PostgreSQL-only constructs.

Changes:

* Added repository runtime storage-engine marker (`storage_engine.go`) set during `InitEnt`, so legacy repositories without config injection can take conservative SQLite branches.
* Replaced security-secret bootstrap `OnConflict(...).DoNothing()` with create-then-handle-unique-conflict logic to avoid relying on PostgreSQL upsert syntax during SQLite startup.
* Replaced setting repository upserts (`OnConflictColumns(...).UpdateNewValues()`) with portable update-then-create logic; `SetMultiple` now loops through `Set` for compatibility.
* Added SQLite branches for API key rate-limit usage updates to avoid PostgreSQL interval/date_trunc expressions.
* Added SQLite branches for usage billing dedup and API key rate-limit updates; account quota JSONB mutation is explicitly degraded in SQLite MVP instead of executing PostgreSQL JSONB functions.
* Added SQLite branches for account extra mutations (`UpdateExtra`, model rate limit set/clear, quota reset) using Ent read-modify-write where practical; account quota increment is degraded to no-op for the SQLite MVP until JSON mutation semantics are ported.
* Expanded unique constraint error detection to include SQLite-style unique constraint messages.

Validation:

* `git diff --check` passes.
* Brace-balance script passes for files touched in this pass.

Remaining known risks:

* SQLite MVP degrades account quota enforcement in JSON `extra` fields.
* Ent-generated SQL and remaining deep reporting/analytics raw SQL still require real SQLite smoke tests.
* Go/gofmt/test validation still blocked by missing Go toolchain on this machine.

## Setup/SQLite First-Run Remediation

Addressed evaluator P0 around first-run setup:

* `initializeDatabase` now branches for SQLite and calls the runtime SQLite/Ent initialization path instead of PostgreSQL SQL migrations.
* `createAdminUser` now has a SQLite path using Ent queries/creates rather than raw SQL after SQLite schema initialization.
* Setup runtime config now derives from `config.LoadForBootstrap()` defaults and then overlays setup fields, so `InitEnt` validation has the same defaults as normal server startup.
* AutoSetup now defaults to SQLite configuration and skips external database preflight when SQLite is selected.
* `writeConfigFile` now writes a `sqlite:` section with snake_case YAML keys.
* Web setup DB validation now treats empty engine as SQLite and accepts SQLite database file paths.
* Web setup only validates Redis host/port/db when `redis.enabled=true`.
* CLI setup defaults to SQLite wording and SQLite file defaults, and makes legacy Redis optional/disabled by default.
* `ensureSimpleModeAdminConcurrency` no longer relies on Ent `OnConflictColumns(...).UpdateNewValues()`.

Validation:

* `git diff --check` passes.
* Brace-balance script passes for setup and simple-mode files.

Still required:

* Run `gofmt`, `go test ./...`, and SQLite first-run smoke test in a Go-enabled environment.

## QA Follow-up Remediation: Idempotency and User Groups

* Added a SQLite branch to `idempotencyRepository.CreateProcessing` that uses plain INSERT, treats unique conflicts as already-created, and queries the created row back without `ON CONFLICT ... RETURNING`.
* Replaced user allowed-group Ent upserts with create-and-ignore-unique-conflict logic in both single add and sync paths.

Validation:

* `git diff --check` passes.
* Brace-balance script passes for `idempotency_repo.go` and `user_repo.go`.

Note:

* PostgreSQL legacy branch still contains `ON CONFLICT ... RETURNING`; SQLite branch bypasses it via `isSQLiteStorage()`.

## QA Follow-up Remediation: Announcement Read and Usage Log Hot Path

* Replaced announcement read `OnConflictColumns(...).DoNothing()` with create-and-ignore-unique-conflict logic.
* Added SQLite usage-log write path:
  * `CreateBestEffort` bypasses batch CTE inserts in SQLite and uses single-row insert.
  * `createBatched` bypasses batch CTE inserts in SQLite and uses single-row insert.
  * `createSingle` uses the SQLite single-row path in SQLite mode.
  * The SQLite single-row path uses plain INSERT without `ON CONFLICT ... RETURNING`; on unique conflict it queries the existing row by `(request_id, api_key_id)`.
  * `execUsageLogInsertNoResult` uses a plain INSERT in SQLite and ignores duplicate request IDs.

Validation:

* `git diff --check` passes.
* Brace-balance script passes for `announcement_read_repo.go` and `usage_log_repo.go`.

Remaining SQLite risks:

* Usage log analytics/reporting SQL still contains PostgreSQL-only constructs; this pass only targets the write hot path.

## Fast Usable MVP Hardening Pass

Prioritized SQLite paths that can block basic admin/account/group usage and gateway usage-log writes.

Changes:

* `announcement_read_repo`: removed Ent conflict clause and now ignores unique conflicts.
* `usage_log_repo`: SQLite mode bypasses batch CTE insert paths and uses plain single-row INSERT; duplicate `(request_id, api_key_id)` is handled by unique-conflict detection + lookup.
* `group_repo`: added SQLite branches for `ExistsByIDs`, `GetAccountCount`, `loadAccountCounts`, `GetAccountIDsByGroupIDs`, `BindAccountsToGroup`, and `UpdateSortOrders`, avoiding `ANY($1)`, `FILTER`, `unnest`, and `ON CONFLICT` on these paths.
* `user_group_rate_repo`: added SQLite paths for batch gets and sync/upsert operations, replacing `ANY`, `unnest`, and `ON CONFLICT` with looped simple SQL.
* `account_repo`: added SQLite branch for `BulkUpdate`, replacing `ANY` and JSONB merge SQL with Ent read/modify/write loops.

Validation:

* `git diff --check` passes.
* Brace-balance script passes for the touched repository files.

MVP trade-off:

* SQLite paths favor correctness/compatibility over batch performance.
* Advanced analytics/dashboard SQL remains PostgreSQL-oriented and still needs later porting or SQLite-specific fallbacks.

## Build/Test QA Update

After Go toolchain was installed:

* `gofmt` ran on changed Go files.
* `go build ./cmd/server` passes.
* `go test ./...` passes after aligning config tests with SQLite-only defaults (`database.sslmode=disable`, Redis validation only when `redis.enabled=true`).
* `git diff --check` passes.

Remaining before commit:

* Ensure untracked SQLite/no-op repository files are added to git.
* Run an actual SQLite AutoSetup/runtime smoke test.
