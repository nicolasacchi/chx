package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var columnsCmd = &cobra.Command{
	Use:   "columns",
	Short: "Inspect columns (system.columns)",
}

var columnsListCmd = &cobra.Command{
	Use:   "list <db>.<table>",
	Short: "List columns of a table with type + default + codec",
	Args:  cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		db, table, err := splitDBTable(args[0])
		if err != nil {
			return err
		}
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		sql := fmt.Sprintf(`
SELECT
  name,
  type,
  default_kind,
  default_expression,
  compression_codec
FROM system.columns
WHERE database = %s AND table = %s
ORDER BY position
`, sqlString(db), sqlString(table))

		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("columns.list", res.Body, jsonFlag, jqFlag)
	},
}

func splitDBTable(s string) (string, string, error) {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return "", "", fmt.Errorf("expected <db>.<table>, got %q", s)
	}
	db := s[:idx]
	tbl := s[idx+1:]
	if db == "" || tbl == "" {
		return "", "", fmt.Errorf("expected <db>.<table>, got %q", s)
	}
	return db, tbl, nil
}

func init() {
	columnsCmd.AddCommand(columnsListCmd)

	output.Register("columns.list", []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "TYPE", Key: "type", Format: output.Truncate(30)},
		{Header: "DEFAULT KIND", Key: "default_kind"},
		{Header: "DEFAULT", Key: "default_expression", Format: output.Truncate(20)},
		{Header: "CODEC", Key: "compression_codec", Format: output.Truncate(20)},
	}, "data")
}
