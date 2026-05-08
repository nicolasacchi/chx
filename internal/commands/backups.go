package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/output"
)

var servicesBackupsCmd = &cobra.Command{
	Use:   "backups",
	Short: "List backups for a service (Cloud Mgmt API)",
}

var servicesBackupsListCmd = &cobra.Command{
	Use:   "list <service-id>",
	Short: "List backups (most recent first)",
	Args:  cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services/" + args[0] + "/backups")
		if err != nil {
			return err
		}
		body, err := c.Get(ctx(), path)
		if err != nil {
			return err
		}
		return output.PrintData("services.backups.list", body, jsonFlag, jqFlag)
	},
}

func init() {
	servicesBackupsCmd.AddCommand(servicesBackupsListCmd)
	servicesCmd.AddCommand(servicesBackupsCmd)

	output.Register("services.backups.list", []output.ColumnDef{
		{Header: "ID", Key: "id", Format: output.Truncate(8)},
		{Header: "STATUS", Key: "status"},
		{Header: "TYPE", Key: "type"},
		{Header: "STARTED", Key: "startedAt", Format: output.Truncate(19)},
		{Header: "FINISHED", Key: "finishedAt", Format: output.Truncate(19)},
		{Header: "SIZE", Key: "sizeInBytes", Format: output.FormatBytes},
		{Header: "DURATION_S", Key: "durationInSeconds"},
	}, "result")
}
