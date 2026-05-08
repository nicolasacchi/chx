package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var databasesCmd = &cobra.Command{
	Use:   "databases",
	Short: "Inspect databases (system.databases)",
}

var databasesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List databases with table counts",
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
  d.name AS name,
  d.engine AS engine,
  d.uuid AS uuid,
  count(t.name) AS table_count
FROM system.databases d
LEFT JOIN system.tables t ON d.name = t.database
GROUP BY d.name, d.engine, d.uuid
ORDER BY d.name
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("databases.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	databasesCmd.AddCommand(databasesListCmd)

	output.Register("databases.list", []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "ENGINE", Key: "engine"},
		{Header: "UUID", Key: "uuid", Format: output.Truncate(8)},
		{Header: "TABLES", Key: "table_count"},
	}, "data")
}
