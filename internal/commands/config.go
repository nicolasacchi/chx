package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/client"
	"github.com/nicolasacchi/chx/internal/config"
)

// jsonUnmarshalImpl is named to avoid colliding with stdlib `json.Unmarshal` when
// the file uses both. The wrapper jsonUnmarshal() above forwards here.
func jsonUnmarshalImpl(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

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

		// Surface A: SELECT 1
		fmt.Printf("surface_a (SQL):       ")
		if !creds.HasSQL() {
			fmt.Println("not configured")
		} else {
			probeSQL(creds)
		}

		// Surface B: real Cloud Mgmt API probe (Phase 3)
		fmt.Printf("surface_b (Cloud):     ")
		if !creds.HasCloud() {
			fmt.Println("not configured")
		} else {
			probeCloud(creds)
		}
		return nil
	},
}

// probeCloud calls GET /v1/organizations and reports the result.
func probeCloud(creds *config.Credentials) {
	timeout, err := resolvedTimeout()
	if err != nil {
		fmt.Printf("FAIL — bad --timeout: %v\n", err)
		return
	}
	c := client.NewCloudClient(creds.CloudKeyID, creds.CloudKeySecret, creds.CloudOrgID, verboseFlag, timeout)
	body, err := c.Get(ctx(), "/organizations")
	if err != nil {
		if api, ok := err.(*client.APIError); ok {
			fmt.Printf("FAIL — HTTP %d: %s\n", api.StatusCode, truncate(api.Body, 200))
			return
		}
		fmt.Printf("FAIL — %v\n", err)
		return
	}
	// Parse the {result, requestId, status} envelope and count orgs.
	var env struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	_ = jsonUnmarshal(body, &env)
	switch len(env.Result) {
	case 0:
		fmt.Println("ok — but no organizations accessible (key may be scoped or expired)")
	case 1:
		fmt.Printf("ok — org=%s (%s)\n", env.Result[0].Name, env.Result[0].ID)
	default:
		names := make([]string, len(env.Result))
		for i, o := range env.Result {
			names[i] = fmt.Sprintf("%s (%s)", o.Name, o.ID)
		}
		fmt.Printf("ok — %d orgs: %s\n", len(env.Result), strings.Join(names, ", "))
	}
}

// jsonUnmarshal is a thin wrapper kept to match the import-light pattern
// of stx (no json package directly in commands when avoidable).
func jsonUnmarshal(b []byte, v any) error {
	return jsonUnmarshalImpl(b, v)
}

// truncate caps a string at n chars (no ellipsis — for short error bodies).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// probeSQL runs SELECT 1 with a short timeout. On TCP/TLS failure, fetches the
// caller's public IP from api.ipify.org and prints an actionable IP-allowlist hint.
func probeSQL(creds *config.Credentials) {
	timeout, err := resolvedTimeout()
	if err != nil {
		fmt.Printf("FAIL — bad --timeout: %v\n", err)
		return
	}
	c := client.NewSQLClient(
		creds.Host, creds.Port, creds.Secure,
		creds.SQLUser, creds.SQLPass, creds.Database,
		verboseFlag, timeout,
	)
	res, err := c.Query(ctx(), "SELECT 1 AS ok", client.SQLOptions{
		Format:        "JSON",
		MaxResultRows: 1,
	})
	if err == nil {
		fmt.Printf("ok — query_id=%s, %d rows in %s\n",
			res.QueryID, res.Summary.ResultRows,
			time.Duration(res.Summary.ElapsedNS)*time.Nanosecond)
		return
	}

	// Connectivity failure → likely IP allowlist or wrong host
	if isConnFail(err) {
		ip := fetchPublicIP(3 * time.Second)
		fmt.Printf("FAIL — TCP/TLS error: %v\n", err)
		if ip != "" {
			fmt.Printf("\n  Your public IP appears to be: %s\n", ip)
			fmt.Printf("  If the SQL endpoint exists but rejects this IP, add it to the allowlist:\n")
			fmt.Printf("    chx services allowlist add <service-id> --ip %s/32 --yes  (Phase 3)\n", ip)
			fmt.Printf("  Or via the ClickHouse Cloud console.\n")
		}
		return
	}

	// SQL-level error (auth, missing grants, etc.)
	if chx, ok := err.(*client.CHException); ok {
		fmt.Printf("FAIL — ClickHouse error %d (%s): %s\n", chx.Code, chx.Name, chx.Message)
		return
	}
	if api, ok := err.(*client.APIError); ok {
		fmt.Printf("FAIL — HTTP %d: %s\n", api.StatusCode, api.Body)
		return
	}
	fmt.Printf("FAIL — %v\n", err)
}

// isConnFail returns true for network/TLS errors (vs. server-rejected requests).
func isConnFail(err error) bool {
	if err == nil {
		return false
	}
	// Wrap unwraps net.OpError, url.Error, etc.
	for ; err != nil; err = errors.Unwrap(err) {
		switch err.(type) {
		case *net.OpError, *net.DNSError:
			return true
		}
		s := err.Error()
		if strings.Contains(s, "connection refused") ||
			strings.Contains(s, "no such host") ||
			strings.Contains(s, "i/o timeout") ||
			strings.Contains(s, "tls:") ||
			strings.Contains(s, "TLS handshake") {
			return true
		}
	}
	return false
}

// fetchPublicIP calls api.ipify.org with a short timeout and returns the IP string.
// Returns "" on any failure (best-effort hint, not a blocker).
func fetchPublicIP(timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return ""
	}
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
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
