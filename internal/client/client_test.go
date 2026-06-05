package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRetryMethodFor(t *testing.T) {
	if got := retryMethodFor(false); got != http.MethodGet {
		t.Errorf("read: got %q, want GET (idempotent → retryable)", got)
	}
	if got := retryMethodFor(true); got != http.MethodPost {
		t.Errorf("write: got %q, want POST (non-idempotent → not retried)", got)
	}
}

func TestShouldRetrySQLStatus(t *testing.T) {
	cases := []struct {
		code  int
		write bool
		want  bool
	}{
		{429, false, true},  // read, rate-limited → retry
		{429, true, true},   // write, rate-limited → still retry (rejected pre-exec)
		{500, false, true},  // read, server error → retry
		{503, false, true},  // read, server error → retry
		{500, true, false},  // write, server error → DON'T retry (may have committed)
		{502, true, false},  // write, server error → DON'T retry
		{400, false, false}, // 4xx → terminal
		{404, true, false},  // 4xx → terminal
		{200, false, false}, // success is not a retry case
	}
	for _, tc := range cases {
		if got := shouldRetrySQLStatus(tc.code, tc.write); got != tc.want {
			t.Errorf("shouldRetrySQLStatus(%d, write=%v) = %v, want %v", tc.code, tc.write, got, tc.want)
		}
	}
}

// TestQuery_WriteDoesNotRetry5xx proves the wiring: a --write request that hits a
// 5xx is surfaced after a single attempt (no blind re-send of a mutation).
func TestQuery_WriteDoesNotRetry5xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := newTestSQLClient(t, srv)
	_, err := c.Query(context.Background(), "INSERT INTO t VALUES (1)", SQLOptions{Write: true})
	if err == nil {
		t.Fatal("expected error from 5xx write, got nil")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("write hit server %d times, want 1 (no retry on 5xx for writes)", n)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("status = %d, want 500", apiErr.StatusCode)
	}
}

// TestQuery_ReadRetries5xx is the read counterpart: a SELECT 5xx is retried the
// full MaxRetries+1 times before surfacing. (Backoff sleeps make this the slow
// path, so it's the only retrying integration case.)
func TestQuery_ReadRetries5xx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping retry-backoff integration test in -short mode")
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable"))
	}))
	defer srv.Close()

	c := newTestSQLClient(t, srv)
	_, err := c.Query(context.Background(), "SELECT 1", SQLOptions{})
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if n := atomic.LoadInt32(&hits); n != MaxRetries+1 {
		t.Errorf("read hit server %d times, want %d (initial + %d retries)", n, MaxRetries+1, MaxRetries)
	}
}
