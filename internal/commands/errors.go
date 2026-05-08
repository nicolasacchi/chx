package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var errorsCmd = &cobra.Command{
	Use:   "errors",
	Short: "Inspect error counters (system.errors)",
}

var errorsTopCmd = &cobra.Command{
	Use:   "top",
	Short: "Show errors ranked by occurrence count",
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
  name,
  code,
  value,
  last_error_time,
  last_error_message
FROM system.errors
WHERE value > 0
ORDER BY value DESC
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("errors.top", res.Body, jsonFlag, jqFlag)
	},
}

var warningsCmd = &cobra.Command{
	Use:   "warnings",
	Short: "Inspect server warnings (system.warnings)",
}

var warningsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List server-emitted configuration warnings",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		const sql = `SELECT * FROM system.warnings`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("warnings.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	errorsCmd.AddCommand(errorsTopCmd)
	warningsCmd.AddCommand(warningsListCmd)

	output.Register("errors.top", []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "CODE", Key: "code"},
		{Header: "COUNT", Key: "value"},
		{Header: "LAST", Key: "last_error_time", Format: output.Truncate(19)},
		{Header: "MESSAGE", Key: "last_error_message", Format: output.Truncate(60)},
	}, "data")

	output.Register("warnings.list", []output.ColumnDef{
		{Header: "MESSAGE", Key: "message", Format: output.Truncate(120)},
	}, "data")
}
