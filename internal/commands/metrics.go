package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var metricsListFilter string

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Inspect instantaneous metric gauges (system.metrics)",
}

var metricsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List metric values + descriptions",
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
		if metricsListFilter != "" {
			where = " WHERE name ILIKE " + sqlString("%"+metricsListFilter+"%")
		}
		sql := `
SELECT name, value, description
FROM system.metrics` + where + `
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
		return output.PrintData("metrics.list", res.Body, jsonFlag, jqFlag)
	},
}

var (
	eventsTopSince  string
	eventsTopFilter string
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Inspect monotonic event counters (system.events)",
}

var eventsTopCmd = &cobra.Command{
	Use:   "top",
	Short: "Show event counters ranked by value",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}
		// `--since` is documented but not yet implemented (would need delta vs snapshot
		// at start). For v0 the value column is the cumulative count since server start.
		_ = eventsTopSince
		where := ""
		if eventsTopFilter != "" {
			where = " WHERE event ILIKE " + sqlString("%"+eventsTopFilter+"%")
		}
		sql := `
SELECT event, value, description
FROM system.events` + where + `
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
		return output.PrintData("events.top", res.Body, jsonFlag, jqFlag)
	},
}

var asyncMetricsListFilter string

var asyncMetricsCmd = &cobra.Command{
	Use:   "async-metrics",
	Short: "Inspect background-sampled metrics (system.asynchronous_metrics)",
}

var asyncMetricsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List asynchronous metric values + descriptions",
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
		if asyncMetricsListFilter != "" {
			where = " WHERE metric ILIKE " + sqlString("%"+asyncMetricsListFilter+"%")
		}
		sql := `
SELECT metric AS name, value, description
FROM system.asynchronous_metrics` + where + `
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
		return output.PrintData("async_metrics.list", res.Body, jsonFlag, jqFlag)
	},
}

func init() {
	metricsListCmd.Flags().StringVar(&metricsListFilter, "filter", "", "Filter by ILIKE %<filter>% on name")
	metricsCmd.AddCommand(metricsListCmd)

	eventsTopCmd.Flags().StringVar(&eventsTopSince, "since", "", "(reserved for future delta computation; currently shows cumulative)")
	eventsTopCmd.Flags().StringVar(&eventsTopFilter, "filter", "", "Filter by ILIKE %<filter>% on event name")
	eventsCmd.AddCommand(eventsTopCmd)

	asyncMetricsListCmd.Flags().StringVar(&asyncMetricsListFilter, "filter", "", "Filter by ILIKE %<filter>% on metric name")
	asyncMetricsCmd.AddCommand(asyncMetricsListCmd)

	cols := []output.ColumnDef{
		{Header: "NAME", Key: "name"},
		{Header: "VALUE", Key: "value"},
		{Header: "DESCRIPTION", Key: "description", Format: output.Truncate(60)},
	}
	output.Register("metrics.list", cols, "data")
	output.Register("async_metrics.list", cols, "data")

	output.Register("events.top", []output.ColumnDef{
		{Header: "EVENT", Key: "event"},
		{Header: "VALUE", Key: "value"},
		{Header: "DESCRIPTION", Key: "description", Format: output.Truncate(60)},
	}, "data")

	_ = fmt.Sprintf // placeholder if future helpers need fmt
	_ = strings.Join
}
