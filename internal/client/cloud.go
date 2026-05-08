// Cloud Management API client for ClickHouse Cloud.
//
// Wire format:
//
//	<METHOD> https://api.clickhouse.cloud/v1/<path>
//	Authorization: Basic base64(KEY_ID:KEY_SECRET)
//	Content-Type: application/json (when body)
//
// Response envelope (verified live 2026-05-08):
//
//	{
//	  "result": <object | array>,    // payload — object for single GETs, array for lists
//	  "requestId": "<uuid>",          // server-side request ID
//	  "status": <int>                 // mirrors HTTP status code
//	}
//
// The client returns the raw response body; command-level code unwraps `result`.
//
// Rate limit: 10 req / 10 s per key, enforced via CloudRateLimiter (token bucket).
// Retry: 429 + 5xx via Phase-0 backoff helpers, honoring Retry-After.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	cloudBaseURL = "https://api.clickhouse.cloud/v1"
)

// CloudClient talks to api.clickhouse.cloud/v1 with HTTP Basic + rate limiting.
type CloudClient struct {
	http      *http.Client
	keyID     string
	keySecret string
	orgID     string // path-scope org, set explicitly or auto-discovered
	limiter   *CloudRateLimiter
	verbose   bool
}

// NewCloudClient constructs a client. orgID may be empty — call DiscoverOrg()
// before any path-scoped call, or pass the orgID via flag/env/config.
func NewCloudClient(keyID, keySecret, orgID string, verbose bool, timeout time.Duration) *CloudClient {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &CloudClient{
		http:      &http.Client{Timeout: timeout},
		keyID:     keyID,
		keySecret: keySecret,
		orgID:     orgID,
		limiter:   NewCloudRateLimiter(),
		verbose:   verbose,
	}
}

// OrgID returns the configured organization ID (may be empty pre-discovery).
func (c *CloudClient) OrgID() string { return c.orgID }

// SetOrgID sets the organization ID after construction (used by DiscoverOrg).
func (c *CloudClient) SetOrgID(id string) { c.orgID = id }

// DiscoverOrg calls GET /organizations and, if exactly one result is returned,
// sets and returns the orgID. With multiple orgs, returns an error and the
// caller must specify --cloud-org-id.
func (c *CloudClient) DiscoverOrg(ctx context.Context) (string, error) {
	body, err := c.Do(ctx, http.MethodGet, "/organizations", nil)
	if err != nil {
		return "", fmt.Errorf("discover org: %w", err)
	}
	var env struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("parse organizations response: %w", err)
	}
	switch len(env.Result) {
	case 0:
		return "", fmt.Errorf("no organizations accessible with the provided API key")
	case 1:
		c.orgID = env.Result[0].ID
		return c.orgID, nil
	default:
		names := make([]string, len(env.Result))
		for i, o := range env.Result {
			names[i] = fmt.Sprintf("%s (%s)", o.Name, o.ID)
		}
		return "", fmt.Errorf("multiple organizations available; specify --cloud-org-id: %s", strings.Join(names, ", "))
	}
}

// OrgPath prefixes path with /organizations/{org}/ (resolves the lazy scope).
// Errors out if orgID is empty.
func (c *CloudClient) OrgPath(suffix string) (string, error) {
	if c.orgID == "" {
		return "", fmt.Errorf("organization ID required: pass --cloud-org-id, set CHX_CLOUD_ORG_ID, or run config add with cloud_organization_id")
	}
	return "/organizations/" + c.orgID + "/" + strings.TrimPrefix(suffix, "/"), nil
}

// Get is a convenience wrapper around Do.
func (c *CloudClient) Get(ctx context.Context, path string) ([]byte, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

// Patch with a JSON body.
func (c *CloudClient) Patch(ctx context.Context, path string, body any) ([]byte, error) {
	return c.Do(ctx, http.MethodPatch, path, body)
}

// Post with a JSON body.
func (c *CloudClient) Post(ctx context.Context, path string, body any) ([]byte, error) {
	return c.Do(ctx, http.MethodPost, path, body)
}

// Delete with optional JSON body (rare for this API).
func (c *CloudClient) Delete(ctx context.Context, path string, body any) ([]byte, error) {
	return c.Do(ctx, http.MethodDelete, path, body)
}

// Do is the workhorse. path may be absolute (/organizations/...) or relative
// to /v1; both resolve correctly. body is marshalled to JSON if non-nil.
func (c *CloudClient) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	full := cloudBaseURL + ensureLeadingSlash(path)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		// Rate limit BEFORE attempts (including retry attempts) so we don't
		// burst through the quota during a 429-loop.
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}

		if attempt > 0 {
			delay := BackoffDelay(lastErr, attempt)
			if c.verbose {
				fmt.Fprintf(VerboseStderr(), "chx-cloud: retry %d/%d after %s\n", attempt, MaxRetries, delay)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		var reqBody io.Reader
		if len(bodyBytes) > 0 {
			reqBody = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, full, reqBody)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.keyID, c.keySecret)
		req.Header.Set("Accept", "application/json")
		if len(bodyBytes) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx-cloud: %s %s\n", method, full)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !ShouldRetryNetwork(err) {
				return nil, err
			}
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx-cloud: -> %d (%d bytes, tokens=%.1f)\n",
				resp.StatusCode, len(respBody), c.limiter.Tokens())
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, nil
		}

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Method:     method,
			URL:        full,
			Body:       string(respBody),
		}
		if ShouldRetryStatus(resp.StatusCode) && attempt < MaxRetries {
			lastErr = AsRetryable(apiErr, resp.Header)
			continue
		}
		return nil, apiErr
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// ensureLeadingSlash returns "/" + path if path doesn't already start with one.
func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
