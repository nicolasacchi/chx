package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBuildParams_DefaultReadonly(t *testing.T) {
	c := &SQLClient{database: "default"}
	v := c.buildParams(SQLOptions{
		Format:        "JSON",
		QueryID:       "abc123",
		MaxResultRows: 1000,
	})
	want := map[string]string{
		"default_format":                        "JSON",
		"query_id":                              "abc123",
		"http_write_exception_in_output_format": "1",
		"enable_http_compression":               "1",
		"readonly":                              "2",
		"max_result_rows":                       "1000",
		"result_overflow_mode":                  "break",
		"database":                              "default",
	}
	for k, expect := range want {
		if got := v.Get(k); got != expect {
			t.Errorf("param %s: got %q, want %q", k, got, expect)
		}
	}
}

func TestBuildParams_WriteStripsReadonlyAndLimit(t *testing.T) {
	c := &SQLClient{}
	v := c.buildParams(SQLOptions{
		Format:        "JSON",
		QueryID:       "x",
		Write:         true,
		MaxResultRows: 1000,
	})
	if v.Get("readonly") != "" {
		t.Errorf("write mode: readonly should be unset, got %q", v.Get("readonly"))
	}
	if v.Get("max_result_rows") != "" {
		t.Errorf("write mode: max_result_rows should be unset, got %q", v.Get("max_result_rows"))
	}
	if v.Get("result_overflow_mode") != "" {
		t.Errorf("write mode: result_overflow_mode should be unset, got %q", v.Get("result_overflow_mode"))
	}
}

func TestBuildParams_LimitZeroDisablesCap(t *testing.T) {
	c := &SQLClient{}
	v := c.buildParams(SQLOptions{
		Format:        "JSON",
		QueryID:       "x",
		MaxResultRows: 0,
	})
	if v.Get("readonly") != "2" {
		t.Errorf("readonly should still be 2 when --limit 0, got %q", v.Get("readonly"))
	}
	if v.Get("max_result_rows") != "" {
		t.Errorf("max_result_rows should be unset when limit=0, got %q", v.Get("max_result_rows"))
	}
}

func TestBuildParams_DatabaseOverride(t *testing.T) {
	c := &SQLClient{database: "default"}
	v := c.buildParams(SQLOptions{Format: "JSON", QueryID: "x", Database: "analytics"})
	if v.Get("database") != "analytics" {
		t.Errorf("database override: got %q, want analytics", v.Get("database"))
	}
}

func TestBuildParams_ExtraSettings(t *testing.T) {
	c := &SQLClient{}
	v := c.buildParams(SQLOptions{
		Format:  "JSON",
		QueryID: "x",
		ExtraSettings: url.Values{
			"max_execution_time": []string{"30"},
		},
	})
	if v.Get("max_execution_time") != "30" {
		t.Errorf("extra setting not propagated, got %q", v.Get("max_execution_time"))
	}
}

func TestExceptionFromBody_DetectsMidStreamException(t *testing.T) {
	body := []byte(`{"meta":[],"data":[],"rows":0,"exception":"Code: 60. DB::Exception: Table system.foo doesn't exist (UNKNOWN_TABLE) (version 24.5.1)"}`)
	chx := exceptionFromBody(body, "JSON", "qid")
	if chx == nil {
		t.Fatalf("expected CHException, got nil")
	}
	if chx.Code != 60 {
		t.Errorf("code: got %d, want 60", chx.Code)
	}
	if chx.Name != "UNKNOWN_TABLE" {
		t.Errorf("name: got %q, want UNKNOWN_TABLE", chx.Name)
	}
	if chx.QueryID != "qid" {
		t.Errorf("query_id: got %q, want qid", chx.QueryID)
	}
}

func TestExceptionFromBody_NoExceptionReturnsNil(t *testing.T) {
	body := []byte(`{"meta":[],"data":[{"x":1}],"rows":1}`)
	if chx := exceptionFromBody(body, "JSON", "qid"); chx != nil {
		t.Errorf("expected nil, got %+v", chx)
	}
}

func TestExceptionFromBody_NonJSONFormatIgnored(t *testing.T) {
	body := []byte(`whatever`)
	if chx := exceptionFromBody(body, "TabSeparated", "qid"); chx != nil {
		t.Errorf("non-JSON format: expected nil, got %+v", chx)
	}
}

func TestParseExceptionCode(t *testing.T) {
	cases := map[string]int{
		"Code: 60. DB::Exception: Foo": 60,
		"Code: 497. ACCESS_DENIED":     497,
		"no code here":                 0,
		"Code: NaN. wrong":             0,
	}
	for in, want := range cases {
		if got := parseExceptionCode(in); got != want {
			t.Errorf("parseExceptionCode(%q): got %d, want %d", in, got, want)
		}
	}
}

func TestParseExceptionName(t *testing.T) {
	msg := "Code: 60. DB::Exception: Table doesn't exist (UNKNOWN_TABLE) (version 24.5.1)"
	if got := parseExceptionName(msg); got != "UNKNOWN_TABLE" {
		t.Errorf("got %q, want UNKNOWN_TABLE", got)
	}
	// No symbolic name in message
	if got := parseExceptionName("Code: 60. some error"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParseSummary(t *testing.T) {
	header := `{"read_rows":"1","read_bytes":"1","written_rows":"0","written_bytes":"0","total_rows_to_read":"0","result_rows":"1","result_bytes":"4","elapsed_ns":"4505959","memory_usage":"1111711"}`
	sum := parseSummary(header)
	if sum.ReadRows != 1 {
		t.Errorf("read_rows: got %d, want 1", sum.ReadRows)
	}
	if sum.ElapsedNS != 4505959 {
		t.Errorf("elapsed_ns: got %d, want 4505959", sum.ElapsedNS)
	}
	if sum.MemoryUsage != 1111711 {
		t.Errorf("memory_usage: got %d, want 1111711", sum.MemoryUsage)
	}
}

func TestParseSummary_EmptyReturnsZero(t *testing.T) {
	if got := parseSummary(""); (got != CHSummary{}) {
		t.Errorf("empty header: got %+v, want zero value", got)
	}
}

func TestNewQueryID_NonEmptyAndUnique(t *testing.T) {
	a := newQueryID()
	b := newQueryID()
	if a == "" || b == "" {
		t.Errorf("query_id should never be empty: a=%q b=%q", a, b)
	}
	if a == b {
		t.Errorf("query_ids should be unique: a=%q b=%q", a, b)
	}
	if !strings.HasPrefix(a, "chx-") && len(a) != 32 {
		t.Errorf("expected 32-hex-char ID or chx- fallback, got %q (len=%d)", a, len(a))
	}
}

// newTestSQLClient wires an SQLClient at the given test server URL. The server
// URL is split into host:port and the scheme determines secure/plain.
func newTestSQLClient(t *testing.T, srv *httptest.Server) *SQLClient {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	host := u.Hostname()
	port := 80
	if p := u.Port(); p != "" {
		if _, err := url.ParseRequestURI(srv.URL); err == nil {
			// crude parse — httptest always gives numeric ports
			var n int
			for _, r := range p {
				n = n*10 + int(r-'0')
			}
			port = n
		}
	}
	secure := u.Scheme == "https"
	return NewSQLClient(host, port, secure, "u", "p", "", false, 5*time.Second)
}

func TestStream_NoBuffering_PlainBody(t *testing.T) {
	const want = "row1\nrow2\nrow3\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/tab-separated-values")
		w.Header().Set("X-ClickHouse-Summary", `{"result_rows":"3"}`)
		w.WriteHeader(200)
		io.WriteString(w, want)
	}))
	defer srv.Close()

	c := newTestSQLClient(t, srv)
	var buf bytes.Buffer
	sum, err := c.Stream(context.Background(), "SELECT 1", SQLOptions{Format: "TSV", QueryID: "qid"}, &buf)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if buf.String() != want {
		t.Errorf("body mismatch:\n  got:  %q\n  want: %q", buf.String(), want)
	}
	if sum.ResultRows != 3 {
		t.Errorf("summary.ResultRows: got %d, want 3", sum.ResultRows)
	}
}

func TestStream_GzipDecoded(t *testing.T) {
	const payload = "the quick brown fox jumps over the lazy dog\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Confirm client advertises gzip
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Errorf("expected Accept-Encoding: gzip header from client, got %q", r.Header.Get("Accept-Encoding"))
		}
		var gzBuf bytes.Buffer
		gw := gzip.NewWriter(&gzBuf)
		gw.Write([]byte(payload))
		gw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write(gzBuf.Bytes())
	}))
	defer srv.Close()

	c := newTestSQLClient(t, srv)
	var buf bytes.Buffer
	_, err := c.Stream(context.Background(), "SELECT 1", SQLOptions{Format: "TSV", QueryID: "qid"}, &buf)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if buf.String() != payload {
		t.Errorf("gzip-decoded body mismatch:\n  got:  %q\n  want: %q", buf.String(), payload)
	}
}

func TestStream_ErrorStatusReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-ClickHouse-Exception-Code", "60")
		w.WriteHeader(404)
		io.WriteString(w, "Code: 60. DB::Exception: Table x doesn't exist (UNKNOWN_TABLE) (version 24.5.1)")
	}))
	defer srv.Close()

	c := newTestSQLClient(t, srv)
	var buf bytes.Buffer
	_, err := c.Stream(context.Background(), "SELECT 1", SQLOptions{Format: "TSV", QueryID: "qid"}, &buf)
	if err == nil {
		t.Fatalf("expected error for 404, got nil")
	}
	if chx, ok := err.(*CHException); ok {
		if chx.Code != 60 {
			t.Errorf("CHException.Code: got %d, want 60", chx.Code)
		}
		if chx.Name != "UNKNOWN_TABLE" {
			t.Errorf("CHException.Name: got %q, want UNKNOWN_TABLE", chx.Name)
		}
	} else if _, ok := err.(*APIError); !ok {
		t.Errorf("expected *CHException or *APIError, got %T: %v", err, err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf should be empty on error, got %d bytes", buf.Len())
	}
}

func TestCHSummary_Format(t *testing.T) {
	s := CHSummary{ResultRows: 5, ElapsedNS: 4_500_000, MemoryUsage: 2 * 1024 * 1024}
	got := s.Format()
	if !strings.Contains(got, "5 rows") {
		t.Errorf("format missing rows: %q", got)
	}
	if !strings.Contains(got, "ms") && !strings.Contains(got, "µs") && !strings.Contains(got, "us") {
		t.Errorf("format missing duration: %q", got)
	}
	if !strings.Contains(got, "2 MB") {
		t.Errorf("format missing memory: %q", got)
	}
}
