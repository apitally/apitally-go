package internal

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestSamplingKeepsTracesWhoseLowTraceIDBitsFallUnderRate(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleRate = 0.5
	registerForTest(t, server, cfg)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))
	kept, dropped := "0af7651916cd43dd7fffffffffffffff", "0af7651916cd43dd8000000000000000"

	for _, traceID := range []string{kept, dropped, kept} {
		req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
		req.Header.Set("traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
		testutils.Do(t, http.DefaultClient.Do, req)
	}
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	for _, span := range spans {
		assert.Equal(t, kept, trace.TraceID(span.TraceId).String())
	}
}

func TestSampleOnRequestFailsOpenAndAbstentionFallsBackToSampleRate(t *testing.T) {
	for _, tc := range []struct {
		name          string
		callback      func(sdktrace.ReadOnlySpan) (float64, bool)
		exportedSpans int
	}{
		{"abstain", func(sdktrace.ReadOnlySpan) (float64, bool) { return 1, false }, 0},
		{"invalid rate", func(sdktrace.ReadOnlySpan) (float64, bool) { return 2, true }, 1},
		{"panic", func(sdktrace.ReadOnlySpan) (float64, bool) { panic("bug") }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			cfg.SampleRate = 0
			cfg.SampleOnRequest = tc.callback
			registerForTest(t, server, cfg)
			testutils.RecordSlog(t)
			appURL := startTestApp(t, http.HandlerFunc(writeOK))

			testutils.Get(t, appURL+"/items")
			require.NoError(t, Shutdown(context.Background()))

			assert.Len(t, server.Spans(t), tc.exportedSpans)
		})
	}
}

func TestSampleOnResponseSeesFinalAttributesAndCanDropRequests(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleOnResponse = func(span sdktrace.ReadOnlySpan) (float64, bool) {
		for _, kv := range span.Attributes() {
			if kv.Key == "http.response.status_code" && kv.Value.AsInt64() == http.StatusNotFound {
				return 0, true
			}
		}
		return 0, false
	}
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", writeOK)
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/missing")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /items", spans[0].Name)
}
