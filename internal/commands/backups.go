package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/output"
)

var servicesBackupsCmd = &cobra.Command{
	Use:   "backups",
	Short: "Manage backups for a service (Cloud Mgmt API)",
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

// restore flags — kept package-local so they don't leak into the global flag space.
var (
	backupsRestoreName     string
	backupsRestoreProvider string
	backupsRestoreRegion   string
	backupsRestoreTier     string
)

var servicesBackupsRestoreCmd = &cobra.Command{
	Use:   "restore <backup-id>",
	Short: "Restore a backup into a new ClickHouse Cloud service",
	Long: `Create a NEW service initialised from an existing backup.

ClickHouse Cloud does not expose backup contents for download — backups can
only be restored into a new service via this endpoint. The new service will
have a fresh service ID, endpoints, and password.

The standard service-creation fields (name, provider, region, tier) are
REQUIRED. Look them up on the source service first:

    chx services get <source-service-id> --jq 'result | {provider,region,tier}'

Then trigger the restore:

    chx services backups list <source-service-id> --jq '#(status="done").id'
    chx services backups restore <backup-id> \
        --name restored-2026-05-12 \
        --provider aws --region us-east-1 --tier production \
        --yes

The created service starts in 'provisioning'; use 'chx services get <new-id>'
to poll until state=running, or run 'chx services start <new-id>' to wait.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMutating("yes-only", "chx services backups restore"); err != nil {
			return err
		}
		if backupsRestoreName == "" {
			return fmt.Errorf("--name is required")
		}
		if backupsRestoreProvider == "" {
			return fmt.Errorf("--provider is required (aws, gcp, azure)")
		}
		if backupsRestoreRegion == "" {
			return fmt.Errorf("--region is required")
		}
		if backupsRestoreTier == "" {
			return fmt.Errorf("--tier is required (development, production)")
		}
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services")
		if err != nil {
			return err
		}
		body := map[string]any{
			"name":     backupsRestoreName,
			"provider": backupsRestoreProvider,
			"region":   backupsRestoreRegion,
			"tier":     backupsRestoreTier,
			"backupId": args[0],
		}
		resp, err := c.Post(ctx(), path, body)
		if err != nil {
			return err
		}
		if jqFlag != "" {
			filtered, ferr := output.ApplyFilter(resp, jqFlag)
			if ferr != nil {
				return ferr
			}
			resp = filtered
		}
		return output.PrintRaw(resp)
	},
}

func init() {
	servicesBackupsRestoreCmd.Flags().StringVar(&backupsRestoreName, "name", "", "Name for the new service (required)")
	servicesBackupsRestoreCmd.Flags().StringVar(&backupsRestoreProvider, "provider", "", "Cloud provider: aws, gcp, azure (required)")
	servicesBackupsRestoreCmd.Flags().StringVar(&backupsRestoreRegion, "region", "", "Region, e.g. us-east-1, eu-west-1 (required)")
	servicesBackupsRestoreCmd.Flags().StringVar(&backupsRestoreTier, "tier", "", "Service tier: development or production (required)")

	servicesBackupsCmd.AddCommand(servicesBackupsListCmd, servicesBackupsRestoreCmd)
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
