package client

import (
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	// DefaultTimeout: chx default 60s to absorb ClickHouse Cloud cold-boot
	// (services suspend after ~15min idle, first query may take 10–30s to wake).
	DefaultTimeout = 60 * time.Second
	MaxRetries     = 3
)

// retryableErr wraps an APIError that may succeed on retry, with an optional
// server-suggested Retry-After delay.
type retryableErr struct {
	apiErr     *APIError
	retryAfter time.Duration
}

func (r retryableErr) Error() string { return r.apiErr.Error() }

// BackoffDelay computes exponential backoff (1s/2s/4s) + jitter for retry attempt n
// (1-indexed). Honors retryableErr.retryAfter when set (e.g., from a Retry-After header).
func BackoffDelay(lastErr error, attempt int) time.Duration {
	if r, ok := lastErr.(retryableErr); ok && r.retryAfter > 0 {
		return r.retryAfter
	}
	base := time.Duration(1<<(attempt-1)) * time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

// ShouldRetryStatus returns true for 429 and 5xx — the canonical retry set.
func ShouldRetryStatus(code int) bool {
	return code == 429 || (code >= 500 && code < 600)
}

// ShouldRetryNetwork returns true for any non-nil network error (caller
// already filtered context.Canceled / DeadlineExceeded if relevant).
func ShouldRetryNetwork(err error) bool {
	return err != nil
}

// ParseRetryAfter reads a Retry-After response header value as a delta-seconds int.
// HTTP-date form (RFC 7231 §7.1.3) is not handled in v0.
func ParseRetryAfter(s string) time.Duration {
	if s == "" {
		return 0
	}
	if secs, err := strconv.Atoi(s); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// AsRetryable wraps an APIError + Retry-After hint into a retryableErr.
// Both clients (sql.go, cloud.go) use this when looping over BackoffDelay.
func AsRetryable(e *APIError, h http.Header) error {
	return retryableErr{apiErr: e, retryAfter: ParseRetryAfter(h.Get("Retry-After"))}
}

// --- verbose plumbing (matches stx pattern) ---

// VerboseStderr is a var so tests can swap it.
var VerboseStderr = func() io.Writer { return verboseDest }

var verboseDest io.Writer = io.Discard

// SetVerboseDest lets the command layer route verbose output (default: io.Discard).
func SetVerboseDest(w io.Writer) { verboseDest = w }

// --- small helpers ---

// IntStr returns strconv.Itoa(n) — used by callers building URL params.
func IntStr(n int) string { return strconv.Itoa(n) }
