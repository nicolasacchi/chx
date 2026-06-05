// Package client provides HTTP transport helpers shared between sql.go (SQL endpoint)
// and cloud.go (ClickHouse Cloud Management API).
package client

import (
	"fmt"

	"github.com/nicolasacchi/clicore/cierrors"
)

// APIError is an HTTP-level error: non-2xx status from either surface.
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s %s: %d", e.Method, e.URL, e.StatusCode)
}

// ExitCode delegates to the fleet-canonical table (auth=2, validation=3,
// not_found=4, rate_limited=5, else 1). CHException keeps its own ClickHouse
// error-code mapping (also 2/4/1).
func (e *APIError) ExitCode() int {
	return cierrors.ExitCodeFor(e.StatusCode, "")
}

// CHException is a ClickHouse-level error: HTTP 200 but the SQL endpoint
// reported an exception (either via X-ClickHouse-Exception-Code header or
// a __exception__ block embedded in a JSON-format response).
//
// Phase 1 (sql.go) populates Code/Name/Message; Phase 0 ships the type only.
type CHException struct {
	Code    int    // ClickHouse error code (e.g., 60 = unknown table)
	Name    string // e.g., "UNKNOWN_TABLE"
	Message string // server-rendered message
	QueryID string // X-ClickHouse-Query-Id, for grepping system.query_log
}

func (e *CHException) Error() string {
	if e.QueryID != "" {
		return fmt.Sprintf("ClickHouse error %d (%s): %s [query_id=%s]", e.Code, e.Name, e.Message, e.QueryID)
	}
	return fmt.Sprintf("ClickHouse error %d (%s): %s", e.Code, e.Name, e.Message)
}

// ExitCode maps ClickHouse error codes to CLI exit codes.
// 497 = ACCESS_DENIED → 2 (auth-like)
// 60  = UNKNOWN_TABLE → 4 (not-found-like)
// Everything else → 1.
func (e *CHException) ExitCode() int {
	switch e.Code {
	case 497, 192, 193, 194, 195, 196: // ACCESS_DENIED + auth family
		return 2
	case 60, 81, 218, 219: // UNKNOWN_TABLE / DATABASE / DICTIONARY etc
		return 4
	default:
		return 1
	}
}
