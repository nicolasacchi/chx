package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage ~/.config/chx/config.toml",
}

var (
	configAddName         string
	configAddHost         string
	configAddPort         int
	configAddSecure       bool
	configAddSQLUser      string
	configAddSQLPassword  string
	configAddSQLPassStdin bool
	configAddDatabase     string
	configAddCloudOrgID   string
	configAddCloudKeyID   string
	configAddCloudSecret  string
	configAddCloudStdin   bool
	configAddReadonly     bool
)

var configAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or replace a profile block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			cfg = &config.Config{Profiles: map[string]config.Profile{}}
		}

		// Resolve secrets — flag value > stdin > empty (env-var-only flow expects no flag here).
		sqlPass := configAddSQLPassword
		if configAddSQLPassStdin {
			s, err := readStdinSecret("sql password")
			if err != nil {
				return err
			}
			sqlPass = s
		}
		cloudSecret := configAddCloudSecret
		if configAddCloudStdin {
			s, err := readStdinSecret("cloud key secret")
			if err != nil {
				return err
			}
			cloudSecret = s
		}

		port := configAddPort
		if port == 0 {
			port = 8443
		}

		cfg.Profiles[name] = config.Profile{
			Host:                configAddHost,
			Port:                port,
			Secure:              configAddSecure || port == 8443,
			SQLUser:             configAddSQLUser,
			SQLPassword:         sqlPass,
			Database:            configAddDatabase,
			CloudOrganizationID: configAddCloudOrgID,
			CloudKeyID:          configAddCloudKeyID,
			CloudKeySecret:      cloudSecret,
			Readonly:            configAddReadonly,
		}
		if cfg.DefaultProfile == "" {
			cfg.DefaultProfile = name
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		path, _ := config.Path()
		fmt.Printf("saved profile %q to %s\n", name, path)
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured profiles",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg == nil || len(cfg.Profiles) == 0 {
			fmt.Println("(no profiles configured)")
			return nil
		}
		names := make([]string, 0, len(cfg.Profiles))
		for n := range cfg.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			marker := " "
			if n == cfg.DefaultProfile {
				marker = "*"
			}
			p := cfg.Profiles[n]
			surfaces := []string{}
			if p.Host != "" && p.SQLUser != "" {
				surfaces = append(surfaces, "sql")
			}
			if p.CloudOrganizationID != "" && p.CloudKeyID != "" {
				surfaces = append(surfaces, "cloud")
			}
			fmt.Printf("%s %-20s host=%-50s surfaces=%s\n", marker, n, emptyOrValue(p.Host, "(none)"), strings.Join(surfaces, ","))
		}
		return nil
	},
}

var configUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set default profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no config file; run 'chx config add' first")
		}
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile %q not found", name)
		}
		cfg.DefaultProfile = name
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("default profile: %s\n", name)
		return nil
	},
}

var configRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a profile block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no config file")
		}
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile %q not found", name)
		}
		delete(cfg.Profiles, name)
		if cfg.DefaultProfile == name {
			cfg.DefaultProfile = ""
			for n := range cfg.Profiles {
				cfg.DefaultProfile = n
				break
			}
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("removed profile %q\n", name)
		return nil
	},
}

var configCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show resolved credentials (secrets redacted)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCreds()
		if err != nil {
			return err
		}
		fmt.Printf("host:                  %s\n", emptyOrValue(creds.Host, "(none)"))
		fmt.Printf("port:                  %d\n", creds.Port)
		fmt.Printf("secure:                %t\n", creds.Secure)
		fmt.Printf("sql_user:              %s\n", emptyOrValue(creds.SQLUser, "(none)"))
		fmt.Printf("sql_password:          %s\n", redact(creds.SQLPass))
		fmt.Printf("database:              %s\n", emptyOrValue(creds.Database, "(none)"))
		fmt.Printf("readonly:              %t\n", creds.Readonly)
		fmt.Printf("cloud_organization_id: %s\n", emptyOrValue(creds.CloudOrgID, "(none)"))
		fmt.Printf("cloud_key_id:          %s\n", emptyOrValue(creds.CloudKeyID, "(none)"))
		fmt.Printf("cloud_key_secret:      %s\n", redact(creds.CloudKeySecret))
		fmt.Printf("\nsurface_a (SQL):       %s\n", okOrNo(creds.HasSQL()))
		fmt.Printf("surface_b (Cloud):     %s\n", okOrNo(creds.HasCloud()))
		return nil
	},
}

var configDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify both surfaces (SELECT 1 on SQL, GET /v1/organizations on Cloud)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCreds()
		if err != nil {
			return err
		}
		fmt.Printf("profile:               %s\n", emptyOrValue(profileFlag, "(default)"))
		fmt.Printf("host:                  %s:%d\n", emptyOrValue(creds.Host, "(none)"), creds.Port)
		fmt.Printf("surface_a (SQL):       %s — probe deferred to Phase 1 (sql.go)\n", okOrNo(creds.HasSQL()))
		fmt.Printf("surface_b (Cloud):     %s — probe deferred to Phase 3 (cloud.go)\n", okOrNo(creds.HasCloud()))
		fmt.Println("\nPhase 0 stub — full doctor lives behind sql.go + cloud.go landings.")
		return nil
	},
}

func init() {
	configAddCmd.Flags().StringVar(&configAddHost, "host", "", "ClickHouse Cloud SQL host (required for SQL surface)")
	configAddCmd.Flags().IntVar(&configAddPort, "port", 8443, "SQL endpoint port")
	configAddCmd.Flags().BoolVar(&configAddSecure, "secure", true, "Use HTTPS (TLS)")
	configAddCmd.Flags().StringVar(&configAddSQLUser, "sql-user", "", "SQL user (e.g., chx_ro)")
	configAddCmd.Flags().StringVar(&configAddSQLPassword, "sql-password", "", "SQL password (prefer --sql-password-stdin or env)")
	configAddCmd.Flags().BoolVar(&configAddSQLPassStdin, "sql-password-stdin", false, "Read SQL password from stdin")
	configAddCmd.Flags().StringVar(&configAddDatabase, "database", "default", "Default database name")
	configAddCmd.Flags().StringVar(&configAddCloudOrgID, "cloud-org-id", "", "Cloud Organization ID (for Surface B)")
	configAddCmd.Flags().StringVar(&configAddCloudKeyID, "cloud-key-id", "", "Cloud API Key ID")
	configAddCmd.Flags().StringVar(&configAddCloudSecret, "cloud-key-secret", "", "Cloud API Key Secret (prefer --cloud-key-secret-stdin or env)")
	configAddCmd.Flags().BoolVar(&configAddCloudStdin, "cloud-key-secret-stdin", false, "Read Cloud API Key Secret from stdin")
	configAddCmd.Flags().BoolVar(&configAddReadonly, "readonly", true, "Enforce readonly=2 by default (recommended for chx_ro profile)")

	configCmd.AddCommand(
		configAddCmd, configListCmd, configUseCmd,
		configRemoveCmd, configCurrentCmd, configDoctorCmd,
	)
}

// readStdinSecret reads one line from stdin, stripping the trailing newline.
// Used by --sql-password-stdin and --cloud-key-secret-stdin.
func readStdinSecret(label string) (string, error) {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read %s from stdin: %w", label, err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func redact(s string) string {
	if s == "" {
		return "(none)"
	}
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func emptyOrValue(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func okOrNo(b bool) string {
	if b {
		return "configured"
	}
	return "not configured"
}
