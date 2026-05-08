package commands

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var (
	mutationsListRunning bool
	mutationsListFailed  bool
	mutationsListTable   string
)

var mutationsCmd = &cobra.Command{
	Use:   "mutations",
	Short: "Inspect ALTER ... DELETE/UPDATE jobs (system.mutations)",
}

var mutationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List mutations (running, failed, or all)",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		var conds []string
		if mutationsListRunning {
			conds = append(conds, "is_done = 0")
		}
		if mutationsListFailed {
			conds = append(conds, "latest_fail_reason != ''")
		}
		if mutationsListTable != "" {
			db, table, err := splitDBTable(mutationsListTable)
			if err != nil {
				return err
			}
			conds = append(conds, "database = "+sqlString(db))
			conds = append(conds, "table = "+sqlString(table))
		}
		where := ""
		if len(conds) > 0 {
			where = " WHERE " + strings.Join(conds, " AND ")
		}

		sql := `
SELECT
  database,
  table,
  mutation_id,
  command,
  create_time,
  parts_to_do,
  is_done,
  latest_failed_part,
  latest_fail_reason
FROM system.mutations` + where + `
ORDER BY create_time DESC
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("mutations.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	mutationsListCmd.Flags().BoolVar(&mutationsListRunning, "running", false, "Show only mutations with is_done = 0")
	mutationsListCmd.Flags().BoolVar(&mutationsListFailed, "failed", false, "Show only mutations with non-empty latest_fail_reason")
	mutationsListCmd.Flags().StringVar(&mutationsListTable, "table", "", "Filter to <db>.<table>")
	mutationsCmd.AddCommand(mutationsListCmd)

	output.Register("mutations.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "MUTATION", Key: "mutation_id", Format: output.Truncate(24)},
		{Header: "COMMAND", Key: "command", Format: output.Truncate(40)},
		{Header: "TODO", Key: "parts_to_do"},
		{Header: "DONE", Key: "is_done"},
		{Header: "FAIL REASON", Key: "latest_fail_reason", Format: output.Truncate(40)},
	}, "data")
}
