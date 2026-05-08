package commands

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var (
	tablesListDatabase string
	tablesListLike     string
)

var tablesCmd = &cobra.Command{
	Use:   "tables",
	Short: "Inspect tables (system.tables)",
}

var tablesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tables with size + engine",
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
		if tablesListDatabase != "" {
			conds = append(conds, "database = "+sqlString(tablesListDatabase))
		}
		if tablesListLike != "" {
			conds = append(conds, "name LIKE "+sqlString(tablesListLike))
		}
		// Hide internal databases by default unless user explicitly filters
		if tablesListDatabase == "" {
			conds = append(conds, "database NOT IN ('system','INFORMATION_SCHEMA','information_schema')")
		}
		where := ""
		if len(conds) > 0 {
			where = " WHERE " + strings.Join(conds, " AND ")
		}
		sql := `
SELECT
  database,
  name,
  engine,
  total_rows,
  total_bytes,
  partition_key
FROM system.tables` + where + `
ORDER BY total_bytes DESC NULLS LAST
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("tables.list", res.Body, jsonFlag, jqFlag)
	},
}

// sqlString escapes a Go string into a single-quoted ClickHouse string literal.
// Doubles single quotes; does NOT handle backslash escaping.
func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func init() {
	tablesListCmd.Flags().StringVar(&tablesListDatabase, "in-db", "", "Filter by database (default: hides system + INFORMATION_SCHEMA). Renamed from --database to avoid clashing with the global --database (SQL session default).")
	tablesListCmd.Flags().StringVar(&tablesListLike, "like", "", "Filter by name LIKE pattern (e.g. '%events%')")
	tablesCmd.AddCommand(tablesListCmd)

	output.Register("tables.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "NAME", Key: "name"},
		{Header: "ENGINE", Key: "engine"},
		{Header: "ROWS", Key: "total_rows"},
		{Header: "BYTES", Key: "total_bytes", Format: output.FormatBytes},
		{Header: "PARTITION", Key: "partition_key", Format: output.Truncate(30)},
	}, "data")
}
