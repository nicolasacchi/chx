package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var processesCmd = &cobra.Command{
	Use:   "processes",
	Short: "List currently-running queries (system.processes / SHOW PROCESSLIST)",
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
  query_id,
  user,
  client_hostname,
  elapsed,
  memory_usage,
  peak_memory_usage,
  read_rows,
  written_rows,
  query
FROM system.processes
ORDER BY elapsed DESC
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("processes", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	output.Register("processes", []output.ColumnDef{
		{Header: "QUERY ID", Key: "query_id", Format: output.Truncate(12)},
		{Header: "USER", Key: "user"},
		{Header: "ELAPSED", Key: "elapsed"},
		{Header: "MEM", Key: "memory_usage", Format: output.FormatBytes},
		{Header: "PEAK MEM", Key: "peak_memory_usage", Format: output.FormatBytes},
		{Header: "READ ROWS", Key: "read_rows"},
		{Header: "QUERY", Key: "query", Format: output.Truncate(60)},
	}, "data")
}
