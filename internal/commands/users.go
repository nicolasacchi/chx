package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Inspect ClickHouse users (system.users — may be restricted on Cloud)",
}

var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List users with their default DB + roles",
	Long: `List users from system.users.

ClickHouse Cloud may restrict access to system.users for non-admin users.
On permission-denied, chx surfaces the ClickHouse error directly with a
hint that this command requires admin grants.`,
	Args: cobra.NoArgs,
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
  storage,
  auth_type,
  arrayStringConcat(host_names_regexp, ', ') AS host_pattern,
  default_database,
  default_roles_all,
  arrayStringConcat(default_roles_list, ', ') AS default_roles_list,
  arrayStringConcat(default_roles_except, ', ') AS default_roles_except
FROM system.users
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
		return output.PrintData("users.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	usersCmd.AddCommand(usersListCmd)

	output.Register("users.list", []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "STORAGE", Key: "storage"},
		{Header: "AUTH", Key: "auth_type"},
		{Header: "HOST", Key: "host_pattern", Format: output.Truncate(20)},
		{Header: "DEFAULT DB", Key: "default_database"},
		{Header: "ALL ROLES", Key: "default_roles_all"},
		{Header: "ROLES", Key: "default_roles_list", Format: output.Truncate(30)},
	}, "data")
}
