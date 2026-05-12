# chx — ClickHouse Explorer CLI

Read-only-by-default Go CLI for ClickHouse Cloud. Wraps:

- **SQL endpoint** at `<host>:8443` — typed commands over `system.*` tables (parts, mutations, replicas, query_log, …) plus raw `chx sql "<SQL>"`
- **Cloud Management API** at `api.clickhouse.cloud/v1` — services CRUD, allowlist, backups, generic `chx api <METHOD> <PATH>` passthrough

JSON output by default; go-pretty tables on TTY; gjson `--jq` filters; errgroup `chx overview` parallel snapshot.

Family member alongside `stx`, `sgx`, `jx`, `gx`, `ddx`, `kv`, `gumlet`, `ga-cli`, `merchant-cli`, `fmq`.

## Install

```bash
go install github.com/nicolasacchi/chx/cmd/chx@latest
# or from source:
git clone https://github.com/nicolasacchi/chx && cd chx && make install
```

## Configure

Two surfaces, both optional. SQL needs a per-service user/password; Cloud needs an org-level API key (Key ID + Key Secret).

```bash
# Setup a profile (interactive password input via stdin)
chx config add prod \
  --host abc123.eu-central-1.aws.clickhouse.cloud \
  --sql-user chx_ro \
  --sql-password-stdin \
  --cloud-org-id <org-uuid> \
  --cloud-key-id <key-id> \
  --cloud-key-secret-stdin

# Or via env vars (no config file needed)
export CHX_HOST=abc123...clickhouse.cloud
export CHX_USER=chx_ro
export CHX_PASSWORD=...
export CHX_CLOUD_KEY_ID=...
export CHX_CLOUD_KEY_SECRET=...
# CHX_CLOUD_ORG_ID is auto-discovered if exactly one org accessible

# Verify both surfaces
chx config doctor
```

Recommended SQL user setup (run as ClickHouse admin):

```sql
CREATE USER chx_ro IDENTIFIED WITH sha256_password BY '<strong-password>';
GRANT SELECT ON *.* TO chx_ro;
GRANT SELECT ON system.parts, system.mutations, system.merges, system.replicas,
                system.replication_queue, system.processes, system.query_log,
                system.errors, system.metrics, system.events,
                system.asynchronous_metrics, system.settings, system.clusters
  TO chx_ro;
```

Notes:
- **No `SETTINGS readonly = 2` on the user** — `chx` enforces `readonly=2` per-request via URL params; setting it at the user level would block `--write` from working at all.
- For DDL/DML, create a separate `chx_admin` user with appropriate grants and switch profiles: `chx <cmd> --profile admin --write --yes`.
- `system.users` access varies by ClickHouse Cloud tier; `chx users list` gracefully surfaces permission-denied.

## Quickstart

```bash
# Typed commands
chx databases list
chx tables list --in-db default --jq 'data.#.{name:name,bytes:total_bytes}'
chx parts list <db>.<table> --top 20 --by bytes_on_disk
chx mutations list --running
chx replicas list
chx queries log --since 1h --top 10 --by duration_ms

# Raw SQL (server-side cap at --limit 1000 by default)
chx sql "SELECT count() FROM system.tables"
chx sql "SELECT * FROM system.numbers LIMIT 5" --format CSV
chx sql "SELECT 1" -v   # verbose: shows URL params on the wire

# Cloud Management
chx services list
chx services get <id>
chx services start <id> --yes      # polls until state=running
chx services allowlist add <id> --ip 1.2.3.4/32 --description "office" --yes
chx services backups list <id>
chx services backups restore <backup-id> \
    --name restored-$(date +%F) \
    --provider aws --region us-east-1 --tier production --yes

# Logical dump (DDL + data) to a local directory
chx dump --db analytics --output ./backup-$(date +%F)
chx dump --db analytics --tables 'events_%' --format Parquet --compress gzip --output ./events-dump
chx dump --db analytics --schema-only --output ./schema

# Generic passthrough for endpoints not yet typed
chx api GET /organizations
chx api PATCH services/<id>/state --data '{"command":"awake"}' --yes

# Parallel snapshot — combines both surfaces
chx overview
chx overview --jq 'sections.cloud_state.data.#.{name:name,state:state}'
```

## Output formats

| Mode | Format | When |
|---|---|---|
| TTY + table registered + no `--jq` / `--json` | go-pretty ASCII table | default for typed commands at terminal |
| Pipe / `--json` | raw JSON envelope (`{meta, data, rows, statistics}` from ClickHouse) | piped, scripted, agentic |
| `--jq <gjson>` | filtered output (gjson, NOT real jq) | structured projection |
| `--ndjson` | `JSONEachRow` raw stream | huge exports; no `--jq` |
| `--format <X>` | passes through to ClickHouse server | CSV / TSV / Vertical / Pretty / etc |

## Backup & restore

ClickHouse Cloud does **not** expose backup contents for download — `chx services backups list` enumerates platform-managed snapshots, but there is no Cloud API for fetching their bytes. Two practical paths:

| Goal | Command | Notes |
|---|---|---|
| Restore a Cloud-side backup into a **new** service | `chx services backups restore <backup-id> --name … --provider … --region … --tier … --yes` | Calls `POST /services` with `backupId`. Creates a fresh service ID, endpoints, and password. Look up provider/region/tier from `chx services get <source-id>` first. |
| Take a **local logical export** of a database | `chx dump --db <name> --output <dir>` | Iterates `system.tables`, writes `<db>/<table>.sql` (DDL) + `<db>/<table>.<ext>` (data) + a `manifest.json`. Streams via `SQLClient.Stream` so multi-GB tables don't OOM. |

`chx dump` is a logical export — not a point-in-time consistent snapshot. For PIT consistency, rely on Cloud-managed backups + `services backups restore`.

## Known divergences from sibling CLIs

- `chx queries log --query-user X` (renamed from `--user X` to avoid clashing with the global `--user` SQL-auth flag)
- `chx tables list --in-db X` and `chx parts list --in-db X` (same — `--database` would shadow the global `--database` for SQL session default)
- Two HTTP clients (`internal/client/sql.go` + `cloud.go`) since the surfaces have different auth, base URLs, and rate limits — most family CLIs only have one
- Token-bucket rate limiter (`internal/client/ratelimit.go`) for Cloud API at 10 req/10s — net-new for the family

## License

MIT.
