package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/output"
)

var sqlCmd = &cobra.Command{
	Use:   "sql <SQL>",
	Short: "Run a raw SQL query against the SQL endpoint",
	Long: `Run an arbitrary SQL query.

Default: readonly=2 server-side, max_result_rows=<--limit> + result_overflow_mode=break.
For DDL/DML, pass --write (also requires --yes). chx does NOT parse the SQL — server
enforces readonly + grants.

Output: ClickHouse FORMAT JSON envelope by default ({meta, data, rows, statistics}).
gjson --jq operates on the whole buffer (e.g., 'data.0.col', 'data.#.name').`,
	Args: cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if writeFlag {
			if err := requireYes("chx sql --write"); err != nil {
				return err
			}
		}
		c, _, err := getSQLClient()
		if err != nil {
			return err
		}

		opts := client.SQLOptions{
			Format:        resolveFormat(),
			MaxResultRows: limitFlag,
			Write:         writeFlag,
			Database:      databaseFlag,
		}

		res, err := c.Query(ctx(), args[0], opts)
		if err != nil {
			return err
		}
		printTimingFooter(res.Summary)

		if ndjsonFlag {
			return output.PrintRaw(res.Body)
		}
		return output.PrintData("sql", res.Body, jsonFlag, jqFlag)
	},
}

// resolveFormat picks the wire format based on flags.
//
//	--format X    → X
//	--ndjson      → JSONEachRow
//	default       → JSON (envelope) for typed commands and chx sql alike
func resolveFormat() string {
	if formatFlag != "" {
		return formatFlag
	}
	if ndjsonFlag {
		return "JSONEachRow"
	}
	return "JSON"
}

// requireSQLArg is a small helper for messages.
var _ = fmt.Sprintf // (fmt kept live for future error strings)
