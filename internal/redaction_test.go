package internal

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestQueryParametersAreRedactedOnAllExportedSpans(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.MaskQueryParams = []string{"^email$"}
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("test").Start(r.Context(), "call upstream")
		span.SetAttributes(attribute.String("url.full", "https://upstream.example/v1?API_KEY=abc&page=2"))
		span.End()
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items?access%5Ftoken=abc&Email=a@b.c&page=1&api%5Fkey%zz=x")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	assert.Equal(t, "access%5Ftoken=[REDACTED]&Email=[REDACTED]&page=1&api%5Fkey%zz=[REDACTED]", testutils.Attributes(findSpan(t, spans, "GET /items").Attributes)["url.query"])
	assert.Equal(t, "https://upstream.example/v1?API_KEY=[REDACTED]&page=2", testutils.Attributes(findSpan(t, spans, "call upstream").Attributes)["url.full"])
}

func TestCapturedHeadersAreRedacted(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.CaptureRequestHeaders = true
	cfg.MaskHeaders = []string{"^x-internal$"}
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("test").Start(r.Context(), "call upstream")
		span.SetAttributes(attribute.StringSlice("http.request.header.x_api_key", []string{"abc"}))
		span.End()
		w.Header().Set("Location", "/items?token=abc&page=2")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.WriteHeader(http.StatusFound)
	})
	appURL := startTestApp(t, mux)

	req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
	req.Header.Set("Authorization", "Bearer abc")
	req.Header.Set("X-Internal", "1")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	testutils.Do(t, client.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	attrs := testutils.Attributes(findSpan(t, spans, "GET /items").Attributes)
	assert.Equal(t, []any{"[REDACTED]"}, attrs["http.request.header.authorization"])
	assert.Equal(t, []any{"[REDACTED]"}, attrs["http.request.header.x-internal"])
	assert.Equal(t, []any{"application/json"}, attrs["http.request.header.accept"])
	assert.Equal(t, []any{strings.TrimPrefix(appURL, "http://")}, attrs["http.request.header.host"])
	assert.Equal(t, []any{"/items?token=[REDACTED]&page=2"}, attrs["http.response.header.location"])
	assert.Equal(t, []any{"[REDACTED]"}, attrs["http.response.header.set-cookie"])
	assert.Equal(t, []any{"[REDACTED]"}, testutils.Attributes(findSpan(t, spans, "call upstream").Attributes)["http.request.header.x_api_key"])
}
