package testutils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Serve serves handler on a local HTTP server until the test ends and
// returns the server's URL.
func Serve(t testing.TB, handler http.Handler) string {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

// Response is an HTTP response read to completion.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       string
}

// Do sends req with roundTrip, such as http.DefaultClient.Do or a Fiber app's
// Test method, and reads the response to completion.
func Do(t testing.TB, roundTrip func(*http.Request) (*http.Response, error), req *http.Request) Response {
	t.Helper()
	resp, err := roundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: string(body)}
}

// Get sends a GET request with http.DefaultClient and the request headers
// given as name and value pairs.
func Get(t testing.TB, url string, headers ...string) Response {
	t.Helper()
	return Send(t, http.MethodGet, url, "", headers...)
}

// Send sends a request with http.DefaultClient and the request headers given
// as name and value pairs.
func Send(t testing.TB, method, url, body string, headers ...string) Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return Do(t, http.DefaultClient.Do, req)
}

// InstrumentHTTPHandler starts a SERVER span named "outer" around next with
// the global tracer provider, as HTTP instrumentation installed outside
// Apitally's middleware does.
func InstrumentHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer("outer-instrumentation").Start(r.Context(), "outer", trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
