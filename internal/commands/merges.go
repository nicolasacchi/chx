package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var mergesCmd = &cobra.Command{
	Use:   "merges",
	Short: "Inspect running merges (system.merges)",
}

var mergesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List currently-running merges with progress",
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
  elapsed,
  progress,
  num_parts,
  source_part_count,
  total_size_bytes_compressed,
  rows_read,
  rows_written
FROM system.merges
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
		return output.PrintData("merges.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	mergesCmd.AddCommand(mergesListCmd)

	output.Register("merges.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "ELAPSED", Key: "elapsed"},
		{Header: "PROGRESS", Key: "progress"},
		{Header: "NUM PARTS", Key: "num_parts"},
		{Header: "SRC PARTS", Key: "source_part_count"},
		{Header: "BYTES", Key: "total_size_bytes_compressed", Format: output.FormatBytes},
	}, "data")
}
