// SQL client for ClickHouse Cloud HTTPS interface (port 8443).
//
// Wire format:
//
//	POST https://<host>:<port>/?<url-params>
//	Headers: X-ClickHouse-User, X-ClickHouse-Key, Accept-Encoding: gzip
//	Body:    raw SQL (UTF-8)
//
// Default URL params injected on every request (allowed under readonly=2):
//
//	default_format=JSON                            (unless caller overrides)
//	query_id=<uuid-v4>                              (for grepping system.query_log)
//	readonly=2                                      (unless --write strips it)
//	http_write_exception_in_output_format=1         (so mid-stream errors land in body)
//	enable_http_compression=1                       (paired with Accept-Encoding: gzip)
//	max_result_rows=<limit>&result_overflow_mode=break  (unless --write or limit=0)
//	database=<name>                                 (when profile sets one)
//
// Error model (two distinct types):
//
//	APIError      — non-2xx HTTP. Surfaces via APIError.Error()/.ExitCode().
//	CHException   — HTTP 200 but the response body has an `exception` field
//	                (FORMAT JSON only — JSONEachRow / NDJSON callers handle it themselves).
//	                Detected by gjson scan of the response body.
package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// SQLOptions configures one SQL request.
//
// Fields with zero values are omitted from the URL params (except the always-on
// settings listed in the package doc). Callers should construct one of these
// per command and pass to Client.Query().
type SQLOptions struct {
	// Format on the wire. Empty → "JSON" (envelope). "JSONEachRow" → NDJSON stream.
	Format string

	// Server-side row cap. 0 = no cap. Default callers should pass 1000.
	// Combined with result_overflow_mode=break for silent truncation.
	MaxResultRows int

	// QueryID is set by the caller (or auto-generated if empty) so the response
	// can later be cross-referenced in system.query_log.
	QueryID string

	// Write disables read-only enforcement (strips readonly=2 + max_result_rows).
	// Caller is responsible for the --yes gate.
	Write bool

	// Database overrides the profile-default DB for this request.
	Database string

	// ExtraSettings is for callers that want to add extra URL params beyond the
	// always-on settings (e.g., session_timeout, max_execution_time). Empty by default.
	ExtraSettings url.Values
}

// SQLResult holds the parsed response of a successful SQL request.
type SQLResult struct {
	Body    []byte     // raw response body (decompressed if gzipped)
	Summary CHSummary  // parsed from X-ClickHouse-Summary header (zero value if absent)
	QueryID string     // X-ClickHouse-Query-Id (server may echo back the one we sent)
	Format  string     // X-ClickHouse-Format (server-confirmed format)
}

// CHSummary parses the X-ClickHouse-Summary response header.
// All fields come over the wire as JSON-encoded strings; we coerce on parse.
type CHSummary struct {
	ReadRows         int64 `json:"read_rows,string"`
	ReadBytes        int64 `json:"read_bytes,string"`
	WrittenRows      int64 `json:"written_rows,string"`
	WrittenBytes     int64 `json:"written_bytes,string"`
	TotalRowsToRead  int64 `json:"total_rows_to_read,string"`
	ResultRows       int64 `json:"result_rows,string"`
	ResultBytes      int64 `json:"result_bytes,string"`
	ElapsedNS        int64 `json:"elapsed_ns,string"`
	MemoryUsage      int64 `json:"memory_usage,string"`
}

// Format renders the summary as a single line for stderr printing under --timing.
func (s CHSummary) Format() string {
	elapsed := time.Duration(s.ElapsedNS) * time.Nanosecond
	return fmt.Sprintf("%d rows in %s, %d MB peak (read %d / written %d)",
		s.ResultRows, elapsed.Round(time.Microsecond),
		s.MemoryUsage/(1024*1024),
		s.ReadRows, s.WrittenRows)
}

// SQLClient talks to the ClickHouse SQL endpoint at <host>:<port>/.
type SQLClient struct {
	http     *http.Client
	user     string
	password string
	baseURL  string // e.g., "https://abc.eu-west-1.aws.clickhouse.cloud:8443"
	database string // profile-default DB
	verbose  bool
}

// NewSQLClient constructs a client. host=hostname only (no scheme).
// secure=true → https://; false → http://. timeout=0 → DefaultTimeout (60s).
func NewSQLClient(host string, port int, secure bool, user, password, database string, verbose bool, timeout time.Duration) *SQLClient {
	scheme := "https"
	if !secure {
		scheme = "http"
	}
	if port == 0 {
		port = 8443
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &SQLClient{
		http:     &http.Client{Timeout: timeout},
		user:     user,
		password: password,
		baseURL:  fmt.Sprintf("%s://%s:%d", scheme, host, port),
		database: database,
		verbose:  verbose,
	}
}

// BaseURL is exposed for diagnostics (config doctor, verbose logs).
func (c *SQLClient) BaseURL() string { return c.baseURL }

// Query sends sql to the server with the given options and returns the parsed result.
// Mid-stream exceptions (FORMAT JSON only) surface as *CHException.
func (c *SQLClient) Query(ctx context.Context, sql string, opts SQLOptions) (*SQLResult, error) {
	if opts.QueryID == "" {
		opts.QueryID = newQueryID()
	}
	if opts.Format == "" {
		opts.Format = "JSON"
	}
	params := c.buildParams(opts)
	full := c.baseURL + "/?" + params.Encode()

	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			delay := BackoffDelay(lastErr, attempt)
			if c.verbose {
				fmt.Fprintf(VerboseStderr(), "chx: retry %d/%d after %s\n", attempt, MaxRetries, delay)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, strings.NewReader(sql))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-ClickHouse-User", c.user)
		req.Header.Set("X-ClickHouse-Key", c.password)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")

		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx: POST %s [query_id=%s]\n", full, opts.QueryID)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !ShouldRetryNetwork(err) {
				return nil, err
			}
			continue
		}

		body, err := readResponseBody(resp)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx: -> %d (%d bytes)\n", resp.StatusCode, len(body))
		}

		// Server-side error (HTTP 4xx/5xx)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			apiErr := &APIError{
				StatusCode: resp.StatusCode,
				Method:     http.MethodPost,
				URL:        full,
				Body:       string(body),
			}
			// 5xx + 429 → retry; 4xx → terminal
			if ShouldRetryStatus(resp.StatusCode) && attempt < MaxRetries {
				lastErr = AsRetryable(apiErr, resp.Header)
				continue
			}
			// Try to upgrade 4xx into a CHException with code/name from headers + body.
			if chx := exceptionFromHeaders(resp.Header, opts.QueryID); chx != nil {
				chx.Message = string(body)
				chx.Name = parseExceptionName(chx.Message)
				return nil, chx
			}
			return nil, apiErr
		}

		// HTTP 200 — but FORMAT JSON may carry an `exception` field at the end.
		if chx := exceptionFromBody(body, opts.Format, opts.QueryID); chx != nil {
			// Augment with exception headers if present
			if name := resp.Header.Get("X-ClickHouse-Exception-Code"); name != "" {
				if code, perr := strconv.Atoi(name); perr == nil {
					chx.Code = code
				}
			}
			return nil, chx
		}

		summary := parseSummary(resp.Header.Get("X-ClickHouse-Summary"))
		serverQID := resp.Header.Get("X-ClickHouse-Query-Id")
		if serverQID == "" {
			serverQID = opts.QueryID
		}
		return &SQLResult{
			Body:    body,
			Summary: summary,
			QueryID: serverQID,
			Format:  resp.Header.Get("X-ClickHouse-Format"),
		}, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// Stream sends sql to the server with the given options and copies the
// (possibly gzip-decoded) response body straight into dst. Unlike Query, it
// never buffers the full body in memory — suitable for multi-GB exports.
//
// Retry policy: only network errors and retryable HTTP statuses (429/5xx) before
// the response body is touched are retried. Once a 200 OK is received, the body
// is committed to dst and any mid-stream error surfaces verbatim.
//
// Error handling on non-2xx: the (small) error body is fully read and returned
// as *APIError or *CHException, same as Query.
//
// The returned CHSummary is populated from the X-ClickHouse-Summary response
// header or trailer (ClickHouse may emit it as either depending on transfer-encoding).
func (c *SQLClient) Stream(ctx context.Context, sql string, opts SQLOptions, dst io.Writer) (CHSummary, error) {
	if opts.QueryID == "" {
		opts.QueryID = newQueryID()
	}
	if opts.Format == "" {
		opts.Format = "JSON"
	}
	params := c.buildParams(opts)
	full := c.baseURL + "/?" + params.Encode()

	var lastErr error
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			delay := BackoffDelay(lastErr, attempt)
			if c.verbose {
				fmt.Fprintf(VerboseStderr(), "chx: stream retry %d/%d after %s\n", attempt, MaxRetries, delay)
			}
			select {
			case <-ctx.Done():
				return CHSummary{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, full, strings.NewReader(sql))
		if err != nil {
			return CHSummary{}, err
		}
		req.Header.Set("X-ClickHouse-User", c.user)
		req.Header.Set("X-ClickHouse-Key", c.password)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")

		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx: POST (stream) %s [query_id=%s]\n", full, opts.QueryID)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if !ShouldRetryNetwork(err) {
				return CHSummary{}, err
			}
			continue
		}

		// Non-2xx: small error body, read fully (matches Query's error path).
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := readResponseBody(resp)
			resp.Body.Close()
			apiErr := &APIError{
				StatusCode: resp.StatusCode,
				Method:     http.MethodPost,
				URL:        full,
				Body:       string(body),
			}
			if ShouldRetryStatus(resp.StatusCode) && attempt < MaxRetries {
				lastErr = AsRetryable(apiErr, resp.Header)
				continue
			}
			if chx := exceptionFromHeaders(resp.Header, opts.QueryID); chx != nil {
				chx.Message = string(body)
				chx.Name = parseExceptionName(chx.Message)
				return CHSummary{}, chx
			}
			return CHSummary{}, apiErr
		}

		// 2xx — committed to streaming. No retry from here.
		r := io.Reader(resp.Body)
		var gz *gzip.Reader
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			gz, err = gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				return CHSummary{}, fmt.Errorf("gzip reader: %w", err)
			}
			r = gz
		}
		_, copyErr := io.Copy(dst, r)
		if gz != nil {
			gz.Close()
		}
		resp.Body.Close()
		if copyErr != nil {
			return CHSummary{}, fmt.Errorf("stream copy: %w", copyErr)
		}

		// Summary may arrive as a header or a trailer depending on transfer-encoding.
		summaryHeader := resp.Header.Get("X-ClickHouse-Summary")
		if summaryHeader == "" && resp.Trailer != nil {
			summaryHeader = resp.Trailer.Get("X-ClickHouse-Summary")
		}
		if c.verbose {
			fmt.Fprintf(VerboseStderr(), "chx: -> %d (streamed)\n", resp.StatusCode)
		}
		return parseSummary(summaryHeader), nil
	}

	return CHSummary{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// buildParams composes the URL params per the always-on setting list.
func (c *SQLClient) buildParams(opts SQLOptions) url.Values {
	v := url.Values{}
	v.Set("default_format", opts.Format)
	v.Set("query_id", opts.QueryID)
	v.Set("http_write_exception_in_output_format", "1")
	v.Set("enable_http_compression", "1")

	if !opts.Write {
		v.Set("readonly", "2")
		if opts.MaxResultRows > 0 {
			v.Set("max_result_rows", strconv.Itoa(opts.MaxResultRows))
			v.Set("result_overflow_mode", "break")
		}
	}

	db := opts.Database
	if db == "" {
		db = c.database
	}
	if db != "" {
		v.Set("database", db)
	}

	for k, vals := range opts.ExtraSettings {
		for _, val := range vals {
			v.Add(k, val)
		}
	}
	return v
}

// readResponseBody decompresses gzip if needed and returns the raw bytes.
func readResponseBody(resp *http.Response) ([]byte, error) {
	r := io.Reader(resp.Body)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gr.Close()
		r = gr
	}
	return io.ReadAll(r)
}

// exceptionFromBody scans a FORMAT JSON envelope for the `exception` field.
// Returns nil if no exception (or format isn't JSON-family).
func exceptionFromBody(body []byte, format, queryID string) *CHException {
	// Only the `JSON` envelope embeds `exception` predictably.
	// JSONEachRow puts it as a final line; the caller for --ndjson handles it.
	switch format {
	case "JSON", "JSONStrings", "JSONColumns", "JSONColumnsWithMetadata", "JSONCompact":
		// fall through
	default:
		return nil
	}
	r := gjson.GetBytes(body, "exception")
	if !r.Exists() {
		return nil
	}
	msg := r.String()
	return &CHException{
		Code:    parseExceptionCode(msg),
		Name:    parseExceptionName(msg),
		Message: msg,
		QueryID: queryID,
	}
}

// exceptionFromHeaders constructs a CHException from response headers when the
// body isn't JSON-family or didn't include the field.
func exceptionFromHeaders(h http.Header, queryID string) *CHException {
	codeStr := h.Get("X-ClickHouse-Exception-Code")
	if codeStr == "" {
		return nil
	}
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return nil
	}
	return &CHException{
		Code:    code,
		QueryID: queryID,
	}
}

// parseExceptionCode extracts the numeric code from a "Code: 60. DB::Exception: ..." string.
func parseExceptionCode(msg string) int {
	const prefix = "Code: "
	idx := strings.Index(msg, prefix)
	if idx < 0 {
		return 0
	}
	rest := msg[idx+len(prefix):]
	end := strings.IndexAny(rest, ".,: ")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// parseExceptionName extracts the symbolic error name (e.g., UNKNOWN_TABLE).
// ClickHouse formats: "Code: 60. DB::Exception: ... (UNKNOWN_TABLE) (version ...)".
func parseExceptionName(msg string) string {
	// Match the LAST parenthesized group that's all-uppercase + underscores
	// (ClickHouse appends "(version X.Y.Z)" too — skip those).
	open := strings.LastIndex(msg, "(")
	for open >= 0 {
		close := strings.Index(msg[open:], ")")
		if close < 0 {
			break
		}
		inner := msg[open+1 : open+close]
		if isUpperSnake(inner) {
			return inner
		}
		// scan back to previous (
		open = strings.LastIndex(msg[:open], "(")
	}
	return ""
}

func isUpperSnake(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || r == '_' || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// parseSummary parses the X-ClickHouse-Summary JSON header value.
func parseSummary(s string) CHSummary {
	if s == "" {
		return CHSummary{}
	}
	var sum CHSummary
	_ = json.Unmarshal([]byte(s), &sum)
	return sum
}

// newQueryID generates a 32-hex-char (16-byte) ID. Not a real UUID, but
// uniquely identifies a query in system.query_log and is small in URL params.
func newQueryID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: timestamp-based — collision-prone but never empty.
		return fmt.Sprintf("chx-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// EnsureSQLBuf is a convenience for callers that want to verify a body is
// non-empty JSON before passing to output.PrintData.
func EnsureSQLBuf(buf []byte) error {
	if len(bytes.TrimSpace(buf)) == 0 {
		return fmt.Errorf("empty response body")
	}
	return nil
}
