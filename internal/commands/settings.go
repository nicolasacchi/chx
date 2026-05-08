package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var settingsListChangedOnly bool

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Inspect ClickHouse runtime settings (system.settings)",
}

var settingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List session/profile settings",
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
		if settingsListChangedOnly {
			where = " WHERE changed = 1"
		}
		sql := `
SELECT name, value, default, changed, description
FROM system.settings` + where + `
ORDER BY name
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("settings.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	settingsListCmd.Flags().BoolVar(&settingsListChangedOnly, "changed", false, "Show only settings whose value differs from default")
	settingsCmd.AddCommand(settingsListCmd)

	output.Register("settings.list", []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "VALUE", Key: "value", Format: output.Truncate(30)},
		{Header: "DEFAULT", Key: "default", Format: output.Truncate(30)},
		{Header: "CHANGED", Key: "changed"},
		{Header: "DESCRIPTION", Key: "description", Format: output.Truncate(50)},
	}, "data")
}
