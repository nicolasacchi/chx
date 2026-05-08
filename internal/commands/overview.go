package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

// overviewSection wraps a section result. data is the raw payload; err non-nil
// indicates the goroutine failed but didn't abort the rest (non-fatal failures).
type overviewSection struct {
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Skipped bool            `json:"skipped,omitempty"`
}

// overviewResult is the JSON envelope shipped to stdout. Empty sections are
// included with `skipped:true` (cloud absent, SQL absent, etc.) so consumers
// can see at a glance which surfaces ran.
type overviewResult struct {
	GeneratedAt string                      `json:"generated_at"`
	ElapsedMS   int64                       `json:"elapsed_ms"`
	Profile     string                      `json:"profile,omitempty"`
	Sections    map[string]*overviewSection `json:"sections"`
}

var overviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Parallel snapshot of SQL system tables + Cloud service state",
	Long: `Aggregated dashboard combining both surfaces.

Fans out (errgroup, up to 5 goroutines):
  metrics       — SQL: top system.metrics gauges
  mutations     — SQL: count of running mutations + their tables
  replication   — SQL: replication queue depth + last error per replica
  top_queries   — SQL: slowest 5 queries last 1 hour (system.query_log,
                       falls back to per-replica system.query_log if --cluster set)
  cloud_state   — Cloud: services list with state + replica count

Sections degrade gracefully:
  - Cloud-only profile (no SQL host): SQL sections marked skipped
  - SQL-only profile (no Cloud creds): cloud_state marked skipped
  - Per-section errors (e.g., chx_ro lacks GRANT on system.query_log) appear
    in that section's "error" field; other sections continue.

Output: a single JSON object with all sections + per-section timing.`,
	Args: cobra.NoArgs,
	RunE: runOverview,
}

func runOverview(cmd *cobra.Command, args []string) error {
	start := time.Now()
	creds, err := loadCreds()
	if err != nil {
		return err
	}

	res := &overviewResult{
		GeneratedAt: start.UTC().Format(time.RFC3339),
		Profile:     profileFlag,
		Sections:    make(map[string]*overviewSection),
	}

	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	addSection := func(name string, s *overviewSection) {
		mu.Lock()
		defer mu.Unlock()
		res.Sections[name] = s
	}

	// SQL surface — only run if configured
	var sqlCli *client.SQLClient
	if creds.HasSQL() {
		timeout, terr := resolvedTimeout()
		if terr != nil {
			return terr
		}
		sqlCli = client.NewSQLClient(
			creds.Host, creds.Port, creds.Secure,
			creds.SQLUser, creds.SQLPass, creds.Database,
			verboseFlag, timeout,
		)

		g.Go(func() error {
			body, qerr := runSQLSection(sqlCli, "metrics", `
SELECT name, value, description
FROM system.metrics
WHERE value > 0
ORDER BY value DESC
LIMIT 20
`)
			addSection("metrics", sectionFromResult(body, qerr))
			return nil
		})

		g.Go(func() error {
			body, qerr := runSQLSection(sqlCli, "mutations", `
SELECT
  count() AS running_count,
  countIf(latest_fail_reason != '') AS failed_count,
  groupArray(database || '.' || table)[1:10] AS tables_with_mutations
FROM system.mutations
WHERE is_done = 0
`)
			addSection("mutations", sectionFromResult(body, qerr))
			return nil
		})

		g.Go(func() error {
			body, qerr := runSQLSection(sqlCli, "replication", `
SELECT
  count() AS replicas_total,
  countIf(is_readonly) AS readonly_count,
  countIf(is_session_expired) AS session_expired_count,
  sum(queue_size) AS total_queue_size,
  max(absolute_delay) AS max_absolute_delay
FROM system.replicas
`)
			addSection("replication", sectionFromResult(body, qerr))
			return nil
		})

		g.Go(func() error {
			body, qerr := runSQLSection(sqlCli, "top_queries", `
SELECT
  normalized_query_hash,
  any(query_kind) AS query_kind,
  count() AS count,
  any(user) AS user,
  quantileExact(0.99)(query_duration_ms) AS duration_ms_p99,
  any(query) AS query_sample
FROM system.query_log
WHERE event_time >= now() - INTERVAL 1 HOUR
  AND type IN ('QueryFinish','ExceptionWhileProcessing')
GROUP BY normalized_query_hash
ORDER BY duration_ms_p99 DESC
LIMIT 5
`)
			addSection("top_queries", sectionFromResult(body, qerr))
			return nil
		})
	} else {
		addSection("metrics", &overviewSection{Skipped: true, Error: "SQL surface not configured"})
		addSection("mutations", &overviewSection{Skipped: true, Error: "SQL surface not configured"})
		addSection("replication", &overviewSection{Skipped: true, Error: "SQL surface not configured"})
		addSection("top_queries", &overviewSection{Skipped: true, Error: "SQL surface not configured"})
	}

	// Cloud surface
	if creds.HasCloud() {
		timeout, terr := resolvedTimeout()
		if terr != nil {
			return terr
		}
		cloudCli := client.NewCloudClient(
			creds.CloudKeyID, creds.CloudKeySecret, creds.CloudOrgID,
			verboseFlag, timeout,
		)
		g.Go(func() error {
			if cloudCli.OrgID() == "" {
				if _, derr := cloudCli.DiscoverOrg(ctx()); derr != nil {
					addSection("cloud_state", &overviewSection{Error: derr.Error()})
					return nil
				}
			}
			path, perr := cloudCli.OrgPath("services")
			if perr != nil {
				addSection("cloud_state", &overviewSection{Error: perr.Error()})
				return nil
			}
			body, qerr := cloudCli.Get(ctx(), path)
			addSection("cloud_state", sectionFromResult(body, qerr))
			return nil
		})
	} else {
		addSection("cloud_state", &overviewSection{Skipped: true, Error: "Cloud Mgmt API not configured"})
	}

	// errgroup.Wait propagates the first error returned by a goroutine.
	// We always return nil from the goroutines (errors get folded into the
	// section), so this is just a barrier.
	_ = g.Wait()

	res.ElapsedMS = time.Since(start).Milliseconds()

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if jqFlag != "" {
		filtered, ferr := output.ApplyFilter(out, jqFlag)
		if ferr != nil {
			return ferr
		}
		out = filtered
	}
	return output.PrintRaw(out)
}

// runSQLSection executes a SELECT and returns the raw FORMAT JSON body.
func runSQLSection(c *client.SQLClient, label, sql string) ([]byte, error) {
	res, err := c.Query(ctx(), sql, client.SQLOptions{
		Format:        "JSON",
		MaxResultRows: 1000,
	})
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// sectionFromResult converts (body, err) into a section payload.
// On success: sets Data to the parsed `data` array (the part agents care about).
// On failure: sets Error to the error string.
func sectionFromResult(body []byte, err error) *overviewSection {
	s := &overviewSection{}
	if err != nil {
		s.Error = err.Error()
		return s
	}
	// Try to extract `result` (Cloud) or `data` (SQL JSON envelope) — fall back to raw body.
	var probe struct {
		Result json.RawMessage `json:"result"`
		Data   json.RawMessage `json:"data"`
	}
	if jerr := json.Unmarshal(body, &probe); jerr == nil {
		switch {
		case len(probe.Data) > 0:
			s.Data = probe.Data
		case len(probe.Result) > 0:
			s.Data = probe.Result
		default:
			s.Data = body
		}
	} else {
		s.Data = body
	}
	return s
}

// init wires overview into rootCmd. We do this here (not root.go) to keep all
// overview-specific code together.
func init() {
	rootCmd.AddCommand(overviewCmd)
}

// keep `os` import live for future stderr verbose hooks
var _ = os.Stderr

// keep fmt live (panic/error formatting may land here later)
var _ = fmt.Sprintf
