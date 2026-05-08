package commands

import (
	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var clustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "Inspect cluster topology (system.clusters)",
}

var clustersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cluster definitions (shards × replicas) with errors_count",
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
  cluster,
  shard_num,
  shard_weight,
  replica_num,
  host_name,
  host_address,
  port,
  is_local,
  errors_count,
  estimated_recovery_time
FROM system.clusters
ORDER BY cluster, shard_num, replica_num
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("clusters.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	clustersCmd.AddCommand(clustersListCmd)

	output.Register("clusters.list", []output.ColumnDef{
		{Header: "CLUSTER", Key: "cluster"},
		{Header: "SHARD", Key: "shard_num"},
		{Header: "REPLICA", Key: "replica_num"},
		{Header: "HOST", Key: "host_name", Format: output.Truncate(40)},
		{Header: "PORT", Key: "port"},
		{Header: "LOCAL", Key: "is_local"},
		{Header: "ERRORS", Key: "errors_count"},
	}, "data")
}
