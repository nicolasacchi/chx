package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/output"
)

// servicesAllowlistEntry mirrors the API shape: {source, description}.
type servicesAllowlistEntry struct {
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// servicesAllowlistDelta is the body shape expected by PATCH /services/{id}.
// Either Add or Remove may be empty; both are sent if both populated.
type servicesAllowlistDelta struct {
	IPAccessList struct {
		Add    []servicesAllowlistEntry `json:"add,omitempty"`
		Remove []servicesAllowlistEntry `json:"remove,omitempty"`
	} `json:"ipAccessList"`
}

var (
	allowlistAddIP          string
	allowlistAddDescription string
	allowlistRemoveIP       string
)

var servicesAllowlistCmd = &cobra.Command{
	Use:   "allowlist",
	Short: "Manage IP allowlist for a service",
}

var servicesAllowlistAddCmd = &cobra.Command{
	Use:   "add <service-id>",
	Short: "Add an IP/CIDR to the service IP allowlist",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMutating("yes-only", "chx services allowlist add"); err != nil {
			return err
		}
		if allowlistAddIP == "" {
			return fmt.Errorf("--ip is required")
		}
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services/" + args[0])
		if err != nil {
			return err
		}
		var delta servicesAllowlistDelta
		delta.IPAccessList.Add = []servicesAllowlistEntry{
			{Source: allowlistAddIP, Description: allowlistAddDescription},
		}
		body, err := c.Patch(ctx(), path, &delta)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ added %s (description: %s) to service %s allowlist\n",
			allowlistAddIP, allowlistAddDescription, args[0])
		if jqFlag != "" {
			filtered, ferr := output.ApplyFilter(body, jqFlag)
			if ferr != nil {
				return ferr
			}
			body = filtered
		}
		return output.PrintRaw(body)
	},
}

var servicesAllowlistRemoveCmd = &cobra.Command{
	Use:   "remove <service-id>",
	Short: "Remove an IP/CIDR from the service IP allowlist",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMutating("yes-only", "chx services allowlist remove"); err != nil {
			return err
		}
		if allowlistRemoveIP == "" {
			return fmt.Errorf("--ip is required")
		}
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		path, err := c.OrgPath("services/" + args[0])
		if err != nil {
			return err
		}
		var delta servicesAllowlistDelta
		delta.IPAccessList.Remove = []servicesAllowlistEntry{
			{Source: allowlistRemoveIP},
		}
		body, err := c.Patch(ctx(), path, &delta)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ removed %s from service %s allowlist\n",
			allowlistRemoveIP, args[0])
		if jqFlag != "" {
			filtered, ferr := output.ApplyFilter(body, jqFlag)
			if ferr != nil {
				return ferr
			}
			body = filtered
		}
		return output.PrintRaw(body)
	},
}

func init() {
	servicesAllowlistAddCmd.Flags().StringVar(&allowlistAddIP, "ip", "", "IP or CIDR to add (e.g. 1.2.3.4/32)")
	servicesAllowlistAddCmd.Flags().StringVar(&allowlistAddDescription, "description", "", "Human-readable label for this allowlist entry")
	servicesAllowlistRemoveCmd.Flags().StringVar(&allowlistRemoveIP, "ip", "", "IP or CIDR to remove (must match the existing entry's source field)")

	servicesAllowlistCmd.AddCommand(servicesAllowlistAddCmd, servicesAllowlistRemoveCmd)
	servicesCmd.AddCommand(servicesAllowlistCmd)
}
