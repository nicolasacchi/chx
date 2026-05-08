package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var (
	queriesLogSince     string
	queriesLogTopN      int
	queriesLogBy        string
	queriesLogUser      string
	queriesLogException bool
)

var queriesCmd = &cobra.Command{
	Use:   "queries",
	Short: "Inspect query history (system.query_log) and kill running queries",
}

var queriesLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show recent queries grouped by normalized hash",
	Long: `Reads system.query_log (or clusterAllReplicas for multi-replica services).

Default filter: WHERE user = currentUser() AND type IN ('QueryFinish','ExceptionWhileProcessing','ExceptionBeforeStart')
              AND event_time >= now() - INTERVAL <since>

Lifts: --all-users (drop user filter), --exception (only failed), --query-user X (override).

Groups by normalized_query_hash. Default LIMIT 1000 rows. ORDER BY duration_ms_p99 DESC.`,
	Args: cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		dur, err := parseSinceDuration(queriesLogSince)
		if err != nil {
			return err
		}
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}

		// Cluster wrapper: by default do NOT use clusterAllReplicas (single-replica
		// services choke on it without cluster definition). Opt-in via --cluster.
		// PersistentFlags default for --cluster is "default"; treat as "no cluster"
		// unless the user passed it explicitly. (Phase 2 simplification — Phase 4
		// overview/health may auto-detect via system.clusters.)
		from := "system.query_log"
		if cmd.Flags().Changed("cluster") && clusterFlag != "" {
			from = fmt.Sprintf("clusterAllReplicas(%s, system.query_log)", sqlString(clusterFlag))
		}

		var conds []string
		conds = append(conds, fmt.Sprintf("event_time >= now() - INTERVAL %d SECOND", int64(dur.Seconds())))
		conds = append(conds, "type IN ('QueryFinish','ExceptionWhileProcessing','ExceptionBeforeStart')")
		if queriesLogException {
			conds = append(conds, "exception_code != 0")
		}
		switch {
		case queriesLogUser != "":
			conds = append(conds, "user = "+sqlString(queriesLogUser))
		case !allUsersFlag:
			conds = append(conds, "user = currentUser()")
		}

		orderCol := "duration_ms_p99"
		switch queriesLogBy {
		case "duration_ms", "duration_ms_p99":
			orderCol = "duration_ms_p99"
		case "count":
			orderCol = "count"
		case "memory":
			orderCol = "max_memory"
		case "rows":
			orderCol = "total_read_rows"
		case "":
			// default
		default:
			return fmt.Errorf("--by must be one of: duration_ms, count, memory, rows")
		}
		topClause := ""
		if queriesLogTopN > 0 {
			topClause = fmt.Sprintf(" LIMIT %d", queriesLogTopN)
		}

		sql := `
SELECT
  normalized_query_hash,
  any(query_kind) AS query_kind,
  count() AS count,
  countIf(exception_code != 0) AS exceptions,
  any(exception_code) AS exception_code,
  any(exception) AS exception_sample,
  quantileExact(0.99)(query_duration_ms) AS duration_ms_p99,
  quantileExact(0.5)(query_duration_ms) AS duration_ms_p50,
  sum(read_rows) AS total_read_rows,
  max(memory_usage) AS max_memory,
  any(user) AS user,
  any(query) AS query_sample
FROM ` + from + `
WHERE ` + strings.Join(conds, " AND ") + `
GROUP BY normalized_query_hash
ORDER BY ` + orderCol + ` DESC` + topClause + `
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("queries.log", res.Body, jsonFlag, jqFlag)
	},
}

var queriesKillCmd = &cobra.Command{
	Use:   "kill <query_id>",
	Short: "Kill a running query (KILL QUERY)",
	Long: `Kills a query by query_id.

Note: ClickHouse only lets a user kill their own queries unless granted
KILL QUERY privilege. With chx_ro (default profile), you can kill only
queries you submitted yourself. To kill another user's stuck query, use
an admin profile (chx queries kill <id> --profile admin --yes) or grant
KILL QUERY to chx_ro (see setup.md).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMutating("yes-only", "chx queries kill"); err != nil {
			return err
		}
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		// KILL QUERY is allowed under any readonly level; pass --write=false but
		// the URL params don't actually block it (it's a system operation, not DML).
		sql := fmt.Sprintf("KILL QUERY WHERE query_id = %s", sqlString(args[0]))
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: 100,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("queries.kill", res.Body, jsonFlag, jqFlag)
	},
}

// parseSinceDuration accepts "1h", "30m", "2d", or a plain Go duration like "1h30m".
func parseSinceDuration(s string) (time.Duration, error) {
	if s == "" {
		return time.Hour, nil
	}
	// Allow "Nd" → N*24h
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since %q: must be Go duration (e.g. 1h, 30m) or Nd", s)
	}
	return d, nil
}

func init() {
	queriesLogCmd.Flags().StringVar(&queriesLogSince, "since", "1h", "Look-back window (e.g. 1h, 30m, 2d)")
	queriesLogCmd.Flags().IntVar(&queriesLogTopN, "top", 0, "Limit to top N grouped rows (0 = no limit, server-side max_result_rows still applies)")
	queriesLogCmd.Flags().StringVar(&queriesLogBy, "by", "duration_ms", "Order by: duration_ms (default), count, memory, rows")
	queriesLogCmd.Flags().StringVar(&queriesLogUser, "query-user", "", "Filter to a specific SQL user (overrides default 'currentUser()'; renamed from --user to avoid clashing with the global SQL-auth --user flag)")
	queriesLogCmd.Flags().BoolVar(&queriesLogException, "exception", false, "Show only queries with exception_code != 0")

	queriesCmd.AddCommand(queriesLogCmd, queriesKillCmd)

	output.Register("queries.log", []output.ColumnDef{
		{Header: "HASH", Key: "normalized_query_hash", Format: output.Truncate(12)},
		{Header: "KIND", Key: "query_kind"},
		{Header: "COUNT", Key: "count"},
		{Header: "ERRS", Key: "exceptions"},
		{Header: "P99 MS", Key: "duration_ms_p99"},
		{Header: "P50 MS", Key: "duration_ms_p50"},
		{Header: "READ", Key: "total_read_rows"},
		{Header: "MEM", Key: "max_memory", Format: output.FormatBytes},
		{Header: "USER", Key: "user"},
		{Header: "QUERY", Key: "query_sample", Format: output.Truncate(50)},
	}, "data")
}
