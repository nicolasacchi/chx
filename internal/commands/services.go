package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tidwall/gjson"

	"github.com/nicolasacchi/chx/internal/output"
)

var servicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Manage ClickHouse Cloud services (Cloud Mgmt API)",
}

var servicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List services in the organization",
	Args:  cobra.NoArgs,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services")
		if err != nil {
			return err
		}
		body, err := c.Get(ctx(), path)
		if err != nil {
			return err
		}
		return output.PrintData("services.list", body, jsonFlag, jqFlag)
	},
}

var servicesGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get full details of one service",
	Args:  cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return validateOutputFlags()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services/" + args[0])
		if err != nil {
			return err
		}
		body, err := c.Get(ctx(), path)
		if err != nil {
			return err
		}
		// `services get` returns a single object under result, so the table
		// renderer can't be used directly — always print as JSON.
		if jqFlag != "" {
			filtered, err := output.ApplyFilter(body, jqFlag)
			if err != nil {
				return err
			}
			body = filtered
		}
		return output.PrintRaw(body)
	},
}

var servicesStartCmd = newServicesStateCmd("start", "Wake the service (PATCH state {command:start}, polls until state=running)")
var servicesStopCmd = newServicesStateCmd("stop", "Suspend the service (PATCH state {command:stop})")
var servicesAwakeCmd = newServicesStateCmd("awake", "Pre-warm the service (PATCH state {command:awake})")

// newServicesStateCmd produces a state-changing services command.
// Polling is only enabled for "start" — stop/awake return immediately and let the
// next operation deal with the new state.
func newServicesStateCmd(command, short string) *cobra.Command {
	return &cobra.Command{
		Use:   command + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMutating("yes-only", "chx services "+command); err != nil {
				return err
			}
			c, _, err := getCloudClient(true)
			if err != nil {
				return err
			}
			path, err := c.OrgPath("services/" + args[0] + "/state")
			if err != nil {
				return err
			}
			body, err := c.Patch(ctx(), path, map[string]string{"command": command})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s sent for service %s\n", command, args[0])

			if command == "start" {
				if err := pollServiceUntilRunning(c, args[0], cmd); err != nil {
					return err
				}
			}

			if jqFlag != "" {
				filtered, err := output.ApplyFilter(body, jqFlag)
				if err != nil {
					return err
				}
				body = filtered
			}
			return output.PrintRaw(body)
		},
	}
}

// pollServiceUntilRunning polls GET /services/{id} every 2s until state=running
// or the timeout from --timeout elapses.
func pollServiceUntilRunning(c interface {
	OrgPath(string) (string, error)
	Get(context.Context, string) ([]byte, error)
}, id string, cmd *cobra.Command) error {
	timeout, err := resolvedTimeout()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	path, err := c.OrgPath("services/" + id)
	if err != nil {
		return err
	}
	last := ""
	for time.Now().Before(deadline) {
		body, err := c.Get(ctx(), path)
		if err != nil {
			return err
		}
		state := gjson.GetBytes(body, "result.state").String()
		if state != last {
			fmt.Fprintf(cmd.OutOrStdout(), "  state=%s\n", state)
			last = state
		}
		if state == "running" {
			return nil
		}
		select {
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for service %s to reach state=running (last seen: %s)", id, last)
}

// servicesListRowsPath: services list response is {"result": [...]} — point
// the table renderer at result so it iterates the array.
func init() {
	servicesCmd.AddCommand(
		servicesListCmd, servicesGetCmd,
		servicesStartCmd, servicesStopCmd, servicesAwakeCmd,
	)

	output.Register("services.list", []output.ColumnDef{
		{Header: "ID", Key: "id", Format: output.Truncate(8)},
		{Header: "NAME", Key: "name"},
		{Header: "STATE", Key: "state"},
		{Header: "PROVIDER", Key: "provider"},
		{Header: "REGION", Key: "region"},
		{Header: "REPLICAS", Key: "numReplicas"},
		{Header: "MEM_GB", Key: "minTotalMemoryGb"},
		{Header: "HOST", Key: "endpoints", Format: formatHTTPSEndpoint},
	}, "result")

	_ = json.RawMessage{}
	_ = strings.TrimSpace
}

// formatHTTPSEndpoint renders the endpoints[] array, picking the https entry's
// host:port for display. Falls back to first entry if no https.
func formatHTTPSEndpoint(v any) string {
	raw, ok := v.([]any)
	if !ok {
		return ""
	}
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if proto, _ := m["protocol"].(string); proto == "https" {
			host, _ := m["host"].(string)
			port, _ := m["port"].(float64)
			return fmt.Sprintf("%s:%d", host, int(port))
		}
	}
	if len(raw) > 0 {
		if m, ok := raw[0].(map[string]any); ok {
			host, _ := m["host"].(string)
			port, _ := m["port"].(float64)
			return fmt.Sprintf("%s:%d", host, int(port))
		}
	}
	return ""
}
