package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var (
	partsListActive  bool
	partsListTopN    int
	partsListBy      string
	partsListDB      string
)

var partsCmd = &cobra.Command{
	Use:   "parts",
	Short: "Inspect parts (system.parts, system.detached_parts)",
}

var partsListCmd = &cobra.Command{
	Use:   "list [<table>]",
	Short: "List parts (per-table or across all DBs)",
	Args:  cobra.MaximumNArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		var conds []string
		if len(args) > 0 {
			db, table, err := splitDBTable(args[0])
			if err != nil {
				return err
			}
			conds = append(conds, "database = "+sqlString(db))
			conds = append(conds, "table = "+sqlString(table))
		} else if partsListDB != "" {
			conds = append(conds, "database = "+sqlString(partsListDB))
		} else {
			// Hide system DB parts unless explicitly filtered
			conds = append(conds, "database NOT IN ('system','INFORMATION_SCHEMA','information_schema')")
		}
		if partsListActive {
			conds = append(conds, "active = 1")
		}
		where := ""
		if len(conds) > 0 {
			where = " WHERE " + strings.Join(conds, " AND ")
		}

		orderCol := "bytes_on_disk"
		switch partsListBy {
		case "rows":
			orderCol = "rows"
		case "modification_time":
			orderCol = "modification_time"
		case "bytes_on_disk", "":
			// default
		default:
			return fmt.Errorf("--by must be one of: bytes_on_disk, rows, modification_time")
		}
		topClause := ""
		if partsListTopN > 0 {
			topClause = fmt.Sprintf(" LIMIT %d", partsListTopN)
		}

		sql := `
SELECT
  database,
  table,
  name,
  partition,
  rows,
  bytes_on_disk,
  marks,
  active,
  modification_time
FROM system.parts` + where + `
ORDER BY ` + orderCol + ` DESC` + topClause

		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("parts.list", res.Body, jsonFlag, jqFlag)
	},
}

var partsDetachedCmd = &cobra.Command{
	Use:   "detached",
	Short: "Operations on detached parts (system.detached_parts)",
}

var partsDetachedListCmd = &cobra.Command{
	Use:   "list [<table>]",
	Short: "List detached parts that need ATTACH or cleanup",
	Args:  cobra.MaximumNArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		var conds []string
		if len(args) > 0 {
			db, table, err := splitDBTable(args[0])
			if err != nil {
				return err
			}
			conds = append(conds, "database = "+sqlString(db))
			conds = append(conds, "table = "+sqlString(table))
		}
		where := ""
		if len(conds) > 0 {
			where = " WHERE " + strings.Join(conds, " AND ")
		}
		sql := `
SELECT
  database,
  table,
  reason,
  name,
  partition_id,
  bytes_on_disk
FROM system.detached_parts` + where + `
ORDER BY database, table, name
`
		res, err := c.Query(ctx(), sql, client.SQLOptions{
			Format:        "JSON",
			MaxResultRows: limitFlag,
		})
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)
		return output.PrintData("parts.detached.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	partsListCmd.Flags().BoolVar(&partsListActive, "active", false, "Show only active parts (active = 1)")
	partsListCmd.Flags().IntVar(&partsListTopN, "top", 0, "Limit to top N rows after ordering (0 = no limit)")
	partsListCmd.Flags().StringVar(&partsListBy, "by", "bytes_on_disk", "Order by: bytes_on_disk (default), rows, modification_time")
	partsListCmd.Flags().StringVar(&partsListDB, "in-db", "", "Filter by database (only when no <table> arg). Renamed from --database to avoid clashing with the global --database (SQL session default).")

	partsCmd.AddCommand(partsListCmd, partsDetachedCmd)
	partsDetachedCmd.AddCommand(partsDetachedListCmd)

	output.Register("parts.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "PARTITION", Key: "partition", Format: output.Truncate(20)},
		{Header: "ROWS", Key: "rows"},
		{Header: "BYTES", Key: "bytes_on_disk", Format: output.FormatBytes},
		{Header: "MARKS", Key: "marks"},
		{Header: "ACTIVE", Key: "active"},
		{Header: "MTIME", Key: "modification_time", Format: output.Truncate(19)},
	}, "data")

	output.Register("parts.detached.list", []output.ColumnDef{
		{Header: "DATABASE", Key: "database"},
		{Header: "TABLE", Key: "table"},
		{Header: "REASON", Key: "reason"},
		{Header: "PARTITION ID", Key: "partition_id"},
		{Header: "NAME", Key: "name", Format: output.Truncate(40)},
		{Header: "BYTES", Key: "bytes_on_disk", Format: output.FormatBytes},
	}, "data")
}
