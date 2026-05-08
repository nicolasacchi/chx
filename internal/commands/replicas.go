package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var replicasCmd = &cobra.Command{
	Use:   "replicas",
	Short: "Inspect replica health (system.replicas)",
}

var replicasListCmd = &cobra.Command{
	Use:   "list",
	Short: "List replica state per replicated table",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		const sql = `
SELECT
  database,
  table,
  is_leader,
  can_become_leader,
  is_readonly,
  is_session_expired,
  queue_size,
  inserts_in_queue,
  merges_in_queue,
  log_max_index,
  log_pointer,
  absolute_delay,
  total_replicas,
  active_replicas
FROM system.replicas
ORDER BY queue_size DESC, database, table
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("replicas.list", res.Body, jsonFlag, jqFlag)
	},
}

var (
	replicationQueueErrorsOnly bool
)

var replicationQueueCmd = &cobra.Command{
	Use:   "replication-queue",
	Short: "Inspect replication queue (system.replication_queue)",
}

var replicationQueueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending replication operations per replica",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		where := ""
		if replicationQueueErrorsOnly {
			where = " WHERE last_exception != ''"
		}
		sql := `
SELECT
  database,
  table,
  type,
  source_replica,
  num_tries,
  last_exception,
  last_attempt_time,
  postpone_reason
FROM system.replication_queue` + where + `
ORDER BY last_attempt_time DESC
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("replication_queue.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	replicasCmd.AddCommand(replicasListCmd)

	replicationQueueListCmd.Flags().BoolVar(&replicationQueueErrorsOnly, "errors-only", false, "Show only entries with non-empty last_exception")
	replicationQueueCmd.AddCommand(replicationQueueListCmd)

	output.Register("replicas.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "LEADER", Key: "is_leader"},
		{Header: "READONLY", Key: "is_readonly"},
		{Header: "SESSION_EXP", Key: "is_session_expired"},
		{Header: "QUEUE", Key: "queue_size"},
		{Header: "DELAY_S", Key: "absolute_delay"},
		{Header: "ACTIVE/TOTAL", Key: "active_replicas"},
	}, "data")

	output.Register("replication_queue.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "TYPE", Key: "type"},
		{Header: "SRC REPLICA", Key: "source_replica"},
		{Header: "TRIES", Key: "num_tries"},
		{Header: "LAST EXCEPTION", Key: "last_exception", Format: output.Truncate(50)},
		{Header: "LAST ATTEMPT", Key: "last_attempt_time", Format: output.Truncate(19)},
	}, "data")
}
