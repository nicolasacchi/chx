# CLAUDE.md — chx

Go CLI for ClickHouse Cloud. Single binary, two HTTP clients (SQL endpoint + Cloud Mgmt API), readonly-by-default, JSON output, gjson `--jq` filters, `chx overview` parallel snapshot.

**APIs**:
- [ClickHouse HTTP interface](https://clickhouse.com/docs/interfaces/http) — port 8443 SQL endpoint
- [ClickHouse Cloud Management API](https://clickhouse.com/docs/cloud/manage/api/api-overview) v1 — `api.clickhouse.cloud/v1`

## Authentication — two surfaces

### Surface A — SQL endpoint
- Header auth: `X-ClickHouse-User` + `X-ClickHouse-Key`
- Per-service username/password (managed in ClickHouse via `CREATE USER`)
- Resolution order (first non-empty wins): `--user`/`--password` flag > `CHX_USER`/`CHX_PASSWORD` env > profile.sql_user/sql_password > profile.sql_password_env

### Surface B — Cloud Mgmt API
- HTTP Basic auth: `KEY_ID:KEY_SECRET`
- Org-level Cloud API key (generate at `console.clickhouse.cloud/api-keys`, role: `Developer` for read-only)
- Resolution order: `--cloud-key-id`/`--cloud-key-secret` flag > `CHX_CLOUD_KEY_ID`/`CHX_CLOUD_KEY_SECRET` env > profile.cloud_key_id/cloud_key_secret > profile.cloud_key_secret_env
- Organization ID: `--cloud-org-id` flag > `CHX_CLOUD_ORG_ID` env > profile.cloud_organization_id > **auto-discovered** via `GET /v1/organizations` if exactly one org accessible

### Multi-profile config

`~/.config/chx/config.toml`:

```toml
default_profile = "prod"

[profiles.prod]
host                    = "abc123.eu-central-1.aws.clickhouse.cloud"
port                    = 8443
secure                  = true
sql_user                = "chx_ro"
sql_password_env        = "CHX_PROD_PASSWORD"   # alt: sql_password = "..."
database                = "default"
cloud_organization_id   = "01h..."
cloud_key_id            = "..."
cloud_key_secret_env    = "CHX_PROD_CLOUD_SECRET"
readonly                = true                  # → readonly=2 on the wire

[profiles.admin]
host                    = "abc123.eu-central-1.aws.clickhouse.cloud"
sql_user                = "chx_admin"
sql_password_env        = "CHX_ADMIN_PASSWORD"
# (no cloud creds — this profile is for SQL writes only)
```

Profiles can configure either surface, both, or one — `chx config doctor` reports per-surface status.

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--profile <name>` | `default_profile` from config | Named profile from config.toml |
| `--host` | — | SQL endpoint hostname |
| `--port` | 8443 | SQL endpoint port |
| `--user` | — | SQL user (NOT the same as `--query-user` on `queries log`) |
| `--password` | — | SQL password |
| `--database` | profile default | Default DB for the SQL query (URL param) |
| `--cloud-key-id` | — | Cloud API Key ID |
| `--cloud-key-secret` | — | Cloud API Key Secret |
| `--cloud-org-id` | auto-discover | Cloud Organization ID |
| `--format <fmt>` | — | ClickHouse output format: JSON (default for typed), JSONEachRow, CSV, TSV, Vertical, Pretty, PrettyCompactNoEscapes |
| `--json` | false | Force raw JSON output even on TTY |
| `--ndjson` | false | Shortcut for `--format JSONEachRow` (mutually exclusive with `--jq` / `--format`) |
| `--jq <expr>` | — | gjson filter (NOT real jq) — whole-buffer over JSON `data` |
| `--timing` | false | Print `X-ClickHouse-Summary` footer to stderr |
| `--limit N` | 1000 | Server-side cap via `max_result_rows` (0 disables) |
| `--timeout <dur>` | 60s | HTTP timeout (Cloud cold-boot tolerance) |
| `--yes` | false | Confirm destructive operations |
| `--write` | false | Strip `readonly=2` + `max_result_rows` URL params (server-side grants still enforce). Requires `--yes` for typed write commands. |
| `--all-users` | false | Show all users in `queries log` (default: current SQL user only) |
| `--cluster <name>` | `default` | Cluster name for `clusterAllReplicas` wrapper in `queries log` (only used when explicitly set) |
| `--verbose / -v` | false | Log HTTP requests + URL params + bytes-back to stderr |
| `--protocol <p>` | http | SQL transport: `http` or `native` (port 9440 — unimplemented in v0) |

## URL params auto-injected on every SQL request

Under default (read-only) flags:

```
default_format=JSON
query_id=<32-hex-char>
http_write_exception_in_output_format=1
enable_http_compression=1
readonly=2
max_result_rows=1000
result_overflow_mode=break
database=<X>     (when set)
```

`--write` strips `readonly=2` + `max_result_rows` + `result_overflow_mode`.
`--limit 0` strips `max_result_rows` + `result_overflow_mode` but keeps `readonly=2`.
`--limit N` sets `max_result_rows=N`.

## Commands

### sql

```bash
chx sql "SELECT 1"                                              # JSON envelope
chx sql "SELECT count() FROM system.tables" --jq 'data.0'       # gjson filter
chx sql "SELECT * FROM events" --format CSV --limit 10000      # CSV export, server-side capped
chx sql "INSERT INTO t VALUES (1)" --write --yes                # writes (server-side grants enforce)
```

### Discovery (system schema)

```bash
chx databases list                                              # name, engine, uuid, table_count
chx tables list                                                 # all tables, ordered by bytes desc
chx tables list --in-db analytics                               # filter (NOTE: --in-db, not --database)
chx tables list --like '%events%'
chx columns list <db>.<table>                                   # columns + types + codecs
```

### MergeTree storage

```bash
chx parts list                                                   # all active parts across DBs
chx parts list <db>.<table> --top 20 --by bytes_on_disk         # top 20 by size
chx parts list --in-db default --by modification_time           # sort by mtime
chx parts detached list                                          # broken/detached parts
```

### Background work

```bash
chx mutations list                                              # all mutations
chx mutations list --running                                    # is_done = 0
chx mutations list --failed                                     # latest_fail_reason != ''
chx mutations list --table <db>.<t>
chx merges list                                                 # currently-running merges
```

### Replication

```bash
chx replicas list                                               # per-table replica state
chx replication-queue list                                      # all pending ops
chx replication-queue list --errors-only                        # last_exception != ''
```

### Query observability

```bash
chx processes                                                    # SHOW PROCESSLIST
chx queries log                                                  # last 1h, current user, dedup'd
chx queries log --since 6h --top 20                             # last 6h, top 20 by p99
chx queries log --query-user analyst --exception                # specific user, errors only
chx queries log --all-users                                     # drop user filter
chx queries log --cluster default                               # wrap in clusterAllReplicas('default', ...)
chx queries kill <query_id> --yes                               # KILL QUERY (needs --profile admin if killing other users)
```

### Metrics & errors

```bash
chx metrics list                                                # system.metrics gauges
chx metrics list --filter MarkCache
chx events top                                                  # system.events counters
chx async-metrics list                                          # system.asynchronous_metrics
chx errors top                                                  # system.errors with last_error_message
chx warnings list                                               # server-emitted config warnings
chx settings list --changed                                     # only non-default settings
```

### Access & topology

```bash
chx users list                                                  # system.users (may be Cloud-restricted)
chx clusters list                                               # system.clusters topology
```

### Cloud Mgmt API (Surface B)

```bash
chx services list                                               # all services, state, host
chx services get <id>                                           # full detail
chx services start <id> --yes                                   # PATCH state, polls to running
chx services stop <id> --yes
chx services awake <id> --yes                                   # cold-boot pre-warm

chx services allowlist add <id> --ip 1.2.3.4/32 --description X --yes
chx services allowlist remove <id> --ip 1.2.3.4/32 --yes

chx services backups list <id>                                  # list Cloud-managed backups for a service
chx services backups restore <backup-id> \
    --name restored-svc --provider aws --region us-east-1 \
    --tier production --yes                                     # POST /services with backupId → new service

chx api GET /organizations                                      # generic passthrough (absolute path)
chx api GET services                                            # relative path → /organizations/<org>/services
chx api PATCH services/<id> --from-file allowlist.json --yes
chx api POST <path> --data '{"key":"value"}' --yes
```

### dump — local logical export

```bash
chx dump --db analytics --output ./backup-2026-05-12
chx dump --db analytics --tables 'events_%' --format Parquet --compress gzip --output ./events-dump
chx dump --db analytics --schema-only --output ./schema
```

Iterates `system.tables` for the target database, writes per-table DDL (`SHOW CREATE TABLE` → `<db>/<table>.sql`) and data (`SELECT * FROM <table> FORMAT <fmt>` → `<db>/<table>.<ext>[.gz]`), plus a `manifest.json` at the root. Data is streamed via `SQLClient.Stream` (no in-memory buffering), so multi-GB tables don't OOM chx.

Caveat: this is a **logical** export, not a point-in-time consistent snapshot. ClickHouse Cloud does not expose binary backup contents for download; the only path to a Cloud-managed backup is `chx services backups restore` (POST `/services` with `backupId`), which restores into a brand-new service.

Engines whose data is logical (`Distributed`, `View`, `MaterializedView`, `Merge`, `Null`, `Dictionary`) get their DDL exported but their data skipped — re-importing them duplicates upstream data they don't own.

### overview — both surfaces in parallel

```bash
chx overview                                                    # JSON envelope, all 5 sections
chx overview --jq 'sections.{cloud:cloud_state.data.#.state,sql_skipped:metrics.skipped}'
```

Sections (parallel goroutines via errgroup):

| Section | SQL or Cloud | Source |
|---|---|---|
| `metrics` | SQL | `SELECT name, value, description FROM system.metrics WHERE value > 0 ORDER BY value DESC LIMIT 20` |
| `mutations` | SQL | aggregate from `system.mutations` (`is_done = 0`) |
| `replication` | SQL | aggregate from `system.replicas` |
| `top_queries` | SQL | top-5 by p99 in last 1h from `system.query_log` |
| `cloud_state` | Cloud | `GET /v1/organizations/{org}/services` |

Each section degrades gracefully:
- Surface not configured → `{skipped: true, error: "...not configured"}`
- Surface configured but query failed → `{error: "ClickHouse error 516 (...)..."}`
- Other sections continue running. Single fatal failure does NOT abort the snapshot.

### config

```bash
chx config add <name> --host ... --sql-user ... --sql-password-stdin   # interactive password
chx config list                                                          # profiles, surfaces configured
chx config use <name>                                                    # set default profile
chx config remove <name>
chx config current                                                       # resolved creds (redacted)
chx config doctor                                                        # probe both surfaces, report
```

`config doctor` runs:
- SQL: `SELECT 1` against the configured endpoint; on TLS handshake failure, fetches public IP from `api.ipify.org` and prints actionable allowlist hint
- Cloud: `GET /v1/organizations`; reports org ID + name on success

## Output Format

ClickHouse `FORMAT JSON` envelope:

```json
{
  "meta": [{"name": "col1", "type": "UInt64"}, ...],
  "data": [{"col1": 1, ...}, ...],
  "rows": N,
  "statistics": {"elapsed": 0.001, "rows_read": N, "bytes_read": M}
}
```

`--jq 'data.0.col1'` returns the first row's `col1`. `--jq 'data.#.col1'` returns the array of all values. `--jq 'data.#.{name:name,bytes:total_bytes}'` projects per-row objects (verify on first use; gjson syntax for compound projections is documented but rarely exercised in the family).

Cloud API envelope:

```json
{
  "result": [...] or {...},
  "requestId": "<uuid>",
  "status": 200
}
```

Use `--jq 'result.#.id'` for list endpoints, `--jq 'result.state'` for single GETs.

## Read-only enforcement (defense-in-depth)

| Layer | Mechanism | What `--write` does |
|---|---|---|
| 1 — SQL grants | `GRANT SELECT` only on `chx_ro`; no INSERT/CREATE/etc grants | unchanged (server still rejects DML) |
| 2 — URL param | `readonly=2` on every request (allows SET so we can inject other settings) | stripped + `max_result_rows` cap also stripped |
| 3 — Cloud API role | Developer-role key by default; admin actions need admin key | unchanged (separate Cloud profile if needed) |

For DDL/DML: use a separate `chx_admin` profile with appropriate grants, then `chx <cmd> --profile admin --write --yes`.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Generic API/network error |
| 2 | Auth error (401/403, ClickHouse `ACCESS_DENIED`/`AUTHENTICATION_FAILED`) |
| 3 | Validation (400) |
| 4 | Not found (404, ClickHouse `UNKNOWN_TABLE`/`UNKNOWN_DATABASE`) |
| 5 | Rate limited (429) |

HTTP exit codes use the fleet-canonical table (`clicore/cierrors.ExitCodeFor`); `CHException` keeps its ClickHouse error-code mapping (also 2/4/1).

## HTTP Client

| Setting | SQL endpoint | Cloud Mgmt API |
|---------|--------------|----------------|
| Timeout | 60s (default) | 60s |
| Retries | 3 on 429 + 5xx + network | same |
| Backoff | Exponential 1s/2s/4s + 0–500 ms jitter, honors `Retry-After` | same |
| Rate limit | none | token bucket: 1 token/sec, burst 10 (matches 10 req/10s quota) |
| Compression | `Accept-Encoding: gzip` + `enable_http_compression=1` URL param | none |
| Auth | `X-ClickHouse-User` + `X-ClickHouse-Key` | HTTP Basic (`KEY_ID:KEY_SECRET`) |

## Mid-stream exception detection (SQL only)

Even on HTTP 200, ClickHouse `FORMAT JSON` may include an `exception` field at the end of the body when an error occurs after streaming started. `chx` scans for this via gjson and surfaces it as `*CHException` (separate from `*APIError` for HTTP-level errors).

`*CHException.ExitCode()` maps:
- 497, 192–196 (ACCESS_DENIED + auth family) → exit 2
- 60, 81, 218, 219 (UNKNOWN_TABLE/DATABASE/etc) → exit 4
- everything else → exit 1

## Build

```bash
make install                    # Install to $GOPATH/bin/chx
make build                      # Build to ./bin/chx
make test                       # Unit tests
make lint                       # golangci-lint (if installed)
```

Requires Go 1.22+.

## Project Structure

```
cmd/chx/main.go                          # Entry point, version injection, exit codes
internal/client/
  client.go                              # Shared helpers: SetVerboseDest, BackoffDelay, retry decisions
  errors.go                              # APIError + CHException with ExitCode()
  ratelimit.go                           # Token bucket for Cloud API (golang.org/x/time/rate)
  sql.go                                 # SQL endpoint client (port 8443)
  sql_test.go                            # URL-param composition + exception parsing tests
  cloud.go                               # Cloud Mgmt API client (api.clickhouse.cloud/v1)
internal/config/
  config.go                              # TOML loader, multi-profile, env override
internal/output/
  output.go                              # JSON/table dispatch, gjson --jq, TTY detect
  table.go                               # go-pretty column registry per command key
  format.go                              # FormatBytes, FormatEpochSeconds, Truncate
internal/commands/
  root.go                                # rootCmd + persistent flags + getSQLClient/getCloudClient
  config.go                              # config add/list/use/remove/current/doctor
  sql.go                                 # chx sql "<SQL>"
  databases.go / tables.go / columns.go  # Surface A discovery
  parts.go / mutations.go / merges.go    # MergeTree storage + background work
  replicas.go                            # Replication
  processes.go / queries.go              # Query observability + KILL QUERY
  metrics.go / errors.go / settings.go   # Metrics suite
  users.go / clusters.go                 # Access + topology
  services.go                            # Surface B: services list/get/start/stop/awake
  allowlist.go                           # services allowlist add/remove
  backups.go                             # services backups list + restore (POST /services with backupId)
  dump.go                                # chx dump — logical export (DDL + per-table data via SQLClient.Stream)
  api.go                                 # Generic Cloud API passthrough
  overview.go                            # Parallel snapshot (errgroup, sgx-pattern)
```

## Family conventions

- TOML config matches stx, kv, jx layout (multi-profile + env override)
- gjson `--jq` whole-buffer over the response (matches stx, kv, gumlet — verified `stx/internal/output/output_test.go:8-60`)
- go-pretty TTY tables with per-command column registry (matches stx, jx)
- errgroup parallel `overview` (matches sgx)
- `confirmDestructive`-style `--yes` gate on destructive ops (matches stx)
- Exit codes 0/1/2/4 (auth=2, not-found=4, generic=1)

## Reference URLs

- HTTP interface — <https://clickhouse.com/docs/interfaces/http>
- Permissions for queries (readonly semantics) — <https://clickhouse.com/docs/operations/settings/permissions-for-queries>
- Output formats — <https://clickhouse.com/docs/interfaces/formats>
- System tables — <https://clickhouse.com/docs/operations/system-tables>
- GRANT statement — <https://clickhouse.com/docs/sql-reference/statements/grant>
- Cloud Mgmt API overview — <https://clickhouse.com/docs/cloud/manage/api/api-overview>
- Cloud Mgmt API Swagger — <https://clickhouse.com/docs/cloud/manage/api/swagger>
- Idling behavior — <https://clickhouse.com/docs/cloud/features/autoscaling/idling>
- Official MCP server (for comparison — chx is a strict superset) — <https://github.com/ClickHouse/mcp-clickhouse>
