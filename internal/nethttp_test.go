package internal

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// startTestApp serves handler behind Apitally's net/http middleware, with
// routes taken from http.ServeMux patterns.
func startTestApp(t *testing.T, handler http.Handler) string {
	server := httptest.NewServer(NetHTTPMiddleware(serveMuxRoute)(handler))
	t.Cleanup(server.Close)
	return server.URL
}

func serveMuxRoute(r *http.Request) string {
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func writeOK(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}
