// Package commands wires the cobra CLI for chx.
package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/config"
)

// Persistent flag values. cobra binds these in init().
//
// Layered as in stx, but with the larger surface area chx needs:
//   - Surface A flags: --host, --port, --user, --password, --database
//   - Surface B flags: --cloud-key-id, --cloud-key-secret, --cloud-org-id
//   - Output flags:    --format, --json, --ndjson, --jq, --timing
//   - Behavior flags:  --limit, --timeout, --yes, --write, --all-users, --cluster, --protocol
var (
	profileFlag        string
	hostFlag           string
	portFlag           int
	userFlag           string
	passwordFlag       string
	cloudKeyIDFlag     string
	cloudKeySecretFlag string
	cloudOrgIDFlag     string
	databaseFlag       string

	formatFlag string
	jsonFlag   bool
	ndjsonFlag bool
	jqFlag     string
	timingFlag bool

	limitFlag    int
	timeoutFlag  string
	yesFlag      bool
	writeFlag    bool
	allUsersFlag bool
	clusterFlag  string
	verboseFlag  bool
	protocolFlag string
)

var rootCmd = &cobra.Command{
	Use:           "chx",
	Short:         "chx — ClickHouse Explorer CLI",
	Long:          "Read-only-by-default CLI for ClickHouse Cloud. Wraps the SQL endpoint (port 8443) and the Cloud Management API (api.clickhouse.cloud/v1) with typed system.* commands, gjson --jq filters, and an `overview` parallel snapshot.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// SetVersion is called by main.go before Execute.
func SetVersion(v string) { rootCmd.Version = v }

// Execute runs the root command.
func Execute() error { return rootCmd.Execute() }

func init() {
	pf := rootCmd.PersistentFlags()

	pf.StringVar(&profileFlag, "profile", "", "Named profile from ~/.config/chx/config.toml (overrides default_profile / CHX_PROFILE)")

	// Surface A — SQL
	pf.StringVar(&hostFlag, "host", "", "ClickHouse Cloud SQL host (e.g. xxx.eu-west-1.aws.clickhouse.cloud)")
	pf.IntVar(&portFlag, "port", 0, "SQL endpoint port (default 8443)")
	pf.StringVar(&userFlag, "user", "", "SQL user (overrides CHX_USER / config)")
	pf.StringVar(&passwordFlag, "password", "", "SQL password (overrides CHX_PASSWORD / config). Prefer env vars or stdin.")
	pf.StringVar(&databaseFlag, "database", "", "Default database for queries")

	// Surface B — Cloud Mgmt API
	pf.StringVar(&cloudKeyIDFlag, "cloud-key-id", "", "Cloud API Key ID")
	pf.StringVar(&cloudKeySecretFlag, "cloud-key-secret", "", "Cloud API Key Secret (prefer env vars or stdin)")
	pf.StringVar(&cloudOrgIDFlag, "cloud-org-id", "", "Cloud Organization ID (path scope for /v1/organizations/<id>/...)")

	// Output
	pf.StringVar(&formatFlag, "format", "", "ClickHouse output format on the wire (JSON, JSONEachRow, CSV, TSV, Vertical, ...). Empty = JSON for typed commands.")
	pf.BoolVar(&jsonFlag, "json", false, "Force raw JSON output even on TTY (skip go-pretty rendering)")
	pf.BoolVar(&ndjsonFlag, "ndjson", false, "Shortcut for --format JSONEachRow with raw streaming write. Mutually exclusive with --jq and --format.")
	pf.StringVar(&jqFlag, "jq", "", "gjson filter (NOT real jq) — whole-buffer over JSON `data` array")
	pf.BoolVar(&timingFlag, "timing", false, "Print X-ClickHouse-Summary footer to stderr (off by default)")

	// Behavior
	pf.IntVar(&limitFlag, "limit", 1000, "Server-side cap via max_result_rows (0 disables)")
	pf.StringVar(&timeoutFlag, "timeout", "60s", "HTTP timeout (e.g. 60s, 2m). Default 60s for Cloud cold-boot.")
	pf.BoolVar(&yesFlag, "yes", false, "Confirm destructive operations")
	pf.BoolVar(&writeFlag, "write", false, "Strip readonly=2 + max_result_rows URL params (server-side grants still enforce). Requires --yes for typed write commands.")
	pf.BoolVar(&allUsersFlag, "all-users", false, "Show all users in queries log (default: current SQL user only)")
	pf.StringVar(&clusterFlag, "cluster", "default", "Cluster name for clusterAllReplicas wrapper in queries log")
	pf.BoolVarP(&verboseFlag, "verbose", "v", false, "Log HTTP requests to stderr")
	pf.StringVar(&protocolFlag, "protocol", "http", "SQL transport: http (default) or native (port 9440)")

	rootCmd.AddCommand(
		// Phase 0/1
		configCmd, sqlCmd, databasesCmd, tablesCmd, columnsCmd,
		// Phase 2
		partsCmd, mutationsCmd, mergesCmd,
		replicasCmd, replicationQueueCmd,
		processesCmd, queriesCmd,
		metricsCmd, eventsCmd, asyncMetricsCmd,
		errorsCmd, warningsCmd, settingsCmd,
		usersCmd, clustersCmd,
		// Phase 3
		servicesCmd, apiCmd,
	)

	// Validation across mutually exclusive flag pairs is handled in PersistentPreRunE on
	// each subcommand (Phase 1+) or by leaf commands as they land.
}

// getSQLClient resolves credentials and constructs a SQL client.
// Returns the error if neither --host nor a profile provides SQL config.
func getSQLClient() (*client.SQLClient, *config.Credentials, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, nil, err
	}
	if !creds.HasSQL() {
		return nil, creds, fmt.Errorf("SQL endpoint not configured: set --host + --user + --password (or run 'chx config add')")
	}
	timeout, err := resolvedTimeout()
	if err != nil {
		return nil, creds, err
	}
	c := client.NewSQLClient(
		creds.Host,
		creds.Port,
		creds.Secure,
		creds.SQLUser,
		creds.SQLPass,
		creds.Database,
		verboseFlag,
		timeout,
	)
	return c, creds, nil
}

// printTimingFooter writes the X-ClickHouse-Summary footer to stderr if --timing is set.
func printTimingFooter(s client.CHSummary) {
	if !timingFlag {
		return
	}
	fmt.Fprintln(os.Stderr, s.Format())
}

// getCloudClient resolves credentials and constructs a Cloud Mgmt API client.
// If autoDiscoverOrg is true and orgID is missing, calls GET /organizations to fill it.
func getCloudClient(autoDiscoverOrg bool) (*client.CloudClient, *config.Credentials, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, nil, err
	}
	if !creds.HasCloud() {
		return nil, creds, fmt.Errorf("Cloud Management API not configured: set --cloud-key-id + --cloud-key-secret + --cloud-org-id (or run 'chx config add')")
	}
	timeout, err := resolvedTimeout()
	if err != nil {
		return nil, creds, err
	}
	c := client.NewCloudClient(creds.CloudKeyID, creds.CloudKeySecret, creds.CloudOrgID, verboseFlag, timeout)
	if c.OrgID() == "" && autoDiscoverOrg {
		id, derr := c.DiscoverOrg(ctx())
		if derr != nil {
			return nil, creds, derr
		}
		creds.CloudOrgID = id
	}
	return c, creds, nil
}

// requireYes errors out if --yes wasn't set. Used by `chx sql --write` and any single-gate write.
func requireYes(op string) error {
	if yesFlag {
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s requires --yes\n", op)
	return fmt.Errorf("--yes required for %s", op)
}

// requireMutating gates typed write-commands that need both --write and --yes,
// or just --yes (e.g., Cloud API mutations have no readonly concept).
//
//	mode = "yes-only"  → just requires --yes (Cloud API state changes, queries kill)
//	mode = "yes+write" → requires --yes AND --write (raw chx sql DDL/DML — though
//	                     chx sql does NOT call this; server-side enforcement instead)
func requireMutating(mode, op string) error {
	switch mode {
	case "yes-only":
		return requireYes(op)
	case "yes+write":
		if !writeFlag {
			fmt.Fprintf(os.Stderr, "%s requires --write to lift readonly\n", op)
			return fmt.Errorf("--write required for %s", op)
		}
		return requireYes(op)
	default:
		return fmt.Errorf("internal error: unknown mode %q", mode)
	}
}

// resolverFromFlags collects the persistent flag values into a config.Resolver.
func resolverFromFlags() config.Resolver {
	return config.Resolver{
		Profile:        profileFlag,
		Host:           hostFlag,
		Port:           portFlag,
		User:           userFlag,
		Password:       passwordFlag,
		Database:       databaseFlag,
		CloudOrgID:     cloudOrgIDFlag,
		CloudKeyID:     cloudKeyIDFlag,
		CloudKeySecret: cloudKeySecretFlag,
	}
}

// loadCreds resolves credentials and applies cross-flag verbose plumbing.
func loadCreds() (*config.Credentials, error) {
	creds, err := config.LoadCredentials(resolverFromFlags())
	if err != nil {
		return nil, err
	}
	if verboseFlag {
		client.SetVerboseDest(os.Stderr)
	}
	return creds, nil
}

// resolvedTimeout parses the --timeout flag, returning the default on error.
func resolvedTimeout() (time.Duration, error) {
	d, err := time.ParseDuration(timeoutFlag)
	if err != nil {
		return 0, fmt.Errorf("invalid --timeout %q: %w", timeoutFlag, err)
	}
	return d, nil
}

// validateOutputFlags enforces --ndjson × --jq × --format mutual exclusion.
// Called from PersistentPreRunE on commands that produce row data.
func validateOutputFlags() error {
	if ndjsonFlag && jqFlag != "" {
		return fmt.Errorf("--ndjson and --jq are mutually exclusive (gjson can't operate on a JSONEachRow stream)")
	}
	if ndjsonFlag && formatFlag != "" {
		return fmt.Errorf("--ndjson and --format are mutually exclusive (--ndjson is a shortcut for --format JSONEachRow)")
	}
	if formatFlag == "JSONEachRow" && jqFlag != "" {
		return fmt.Errorf("--jq cannot operate on FORMAT JSONEachRow (not a single JSON document); use FORMAT JSON or remove --jq")
	}
	return nil
}

// ctx returns a request context (Phase 1 will introduce per-call timeouts).
func ctx() context.Context { return context.Background() }
