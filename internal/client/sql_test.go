package client

import (
	"net/url"
	"strings"
	"testing"
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
		"Code: 60. DB::Exception: Foo":               60,
		"Code: 497. ACCESS_DENIED":                   497,
		"no code here":                               0,
		"Code: NaN. wrong":                           0,
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
