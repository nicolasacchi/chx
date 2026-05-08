package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nicolasacchi/chx/internal/output"
)

var (
	apiFromFile string
	apiData     string
)

var apiCmd = &cobra.Command{
	Use:   "api <METHOD> <PATH>",
	Short: "Generic Cloud Management API passthrough",
	Long: `Make a raw call to the ClickHouse Cloud Management API.

Path semantics:
  /v1/...                            → absolute, used as-is
  /organizations/...                 → absolute (without /v1 prefix)
  organizations/...                  → same, leading slash optional
  services                           → relative to /v1/organizations/<org>/
  services/<id>/state                → relative to /v1/organizations/<org>/

Methods supported: GET, POST, PATCH, DELETE.

For methods that take a body, pass either:
  --data '{"command":"start"}'       inline JSON
  --from-file body.json              file path

Output: raw response body (after gzip-decode if applicable). --jq operates
on the whole-buffer response (envelope is {result, requestId, status}).

Examples:
  chx api GET /organizations
  chx api GET services
  chx api GET services/<id>
  chx api PATCH services/<id>/state --data '{"command":"awake"}' --yes
  chx api PATCH services/<id> --from-file allowlist.json --yes`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		method := strings.ToUpper(args[0])
		path := args[1]

		// Mutating verbs require --yes (no SQL readonly involved)
		if method != http.MethodGet {
			if err := requireYes("chx api " + method); err != nil {
				return err
			}
		}

		// Resolve body input
		var bodyVal any
		if apiData != "" && apiFromFile != "" {
			return fmt.Errorf("--data and --from-file are mutually exclusive")
		}
		if apiData != "" {
			if err := json.Unmarshal([]byte(apiData), &bodyVal); err != nil {
				return fmt.Errorf("--data must be valid JSON: %w", err)
			}
		}
		if apiFromFile != "" {
			data, err := os.ReadFile(apiFromFile)
			if err != nil {
				return fmt.Errorf("read --from-file %s: %w", apiFromFile, err)
			}
			if err := json.Unmarshal(data, &bodyVal); err != nil {
				return fmt.Errorf("%s: invalid JSON: %w", apiFromFile, err)
			}
		}

		// Path resolution: scope or pass-through
		c, _, err := getCloudClient(true)
		if err != nil {
			return err
		}
		fullPath, err := resolveAPIPath(path, c.OrgID)
		if err != nil {
			return err
		}

		body, err := c.Do(ctx(), method, fullPath, bodyVal)
		if err != nil {
			return err
		}
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

// resolveAPIPath maps user-supplied path → absolute /v1 path.
// orgIDFn is called only when the path needs scoping (relative).
//
// Recognized as ABSOLUTE (passed through as-is, /v1 already prepended by CloudClient.Do):
//
//	/v1/anything                → strip /v1, prepend /
//	/organizations              → absolute (with or without sub-path)
//	/organizations/...          → absolute
//	organizations               → leading-slash optional
//	organizations/...           → leading-slash optional
//
// Everything else is RELATIVE: scoped under /organizations/<org>/.
func resolveAPIPath(path string, orgIDFn func() string) (string, error) {
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1"), nil
	}
	// Normalize: drop a leading slash for the prefix check
	check := strings.TrimPrefix(path, "/")
	if check == "organizations" || strings.HasPrefix(check, "organizations/") {
		// Always emit a leading slash; CloudClient.Do is forgiving but we keep it consistent.
		if !strings.HasPrefix(path, "/") {
			return "/" + path, nil
		}
		return path, nil
	}
	// Relative — needs orgID
	org := orgIDFn()
	if org == "" {
		return "", fmt.Errorf("relative path %q requires organization ID; pass --cloud-org-id, set CHX_CLOUD_ORG_ID, or use an absolute path", path)
	}
	return "/organizations/" + org + "/" + strings.TrimPrefix(path, "/"), nil
}

func init() {
	apiCmd.Flags().StringVar(&apiFromFile, "from-file", "", "Read JSON request body from file (matches stx convention)")
	apiCmd.Flags().StringVar(&apiData, "data", "", "Inline JSON request body (alternative to --from-file)")
}
