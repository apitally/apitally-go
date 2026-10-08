package internal

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCapturedPayloadsAreExportedOnlyToApitally(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.CaptureRequestHeaders, cfg.CaptureRequestBody, cfg.CaptureResponseBody = true, true, true
	registerForTest(t, server, cfg)
	userSpans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(userSpans)))
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))

	post(t, appURL+"/items", "application/json", `{"name":"x"}`)
	require.NoError(t, Shutdown(context.Background()))

	require.Len(t, userSpans.GetSpans(), 1)
	for _, kv := range userSpans.GetSpans()[0].Attributes {
		assert.NotContains(t, []string{"apitally.request.body", "apitally.response.body"}, string(kv.Key))
		assert.False(t, strings.HasPrefix(string(kv.Key), "http.request.header.") || strings.HasPrefix(string(kv.Key), "http.response.header."), "user span has captured header %s", kv.Key)
	}
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, `{"name":"x"}`, attrs["apitally.request.body"])
	assert.Equal(t, `{"id":1}`, attrs["apitally.response.body"])
	assert.Equal(t, []any{"application/json"}, attrs["http.request.header.content-type"])
	assert.Equal(t, []any{"application/json"}, attrs["http.response.header.content-type"])
}
