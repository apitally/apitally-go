package internal

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestSamplingIsDeterministicByTraceID(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleRate = 0.5
	registerForTest(t, server, cfg)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))
	kept, dropped := "0af7651916cd43dd7fffffffffffffff", "0af7651916cd43dd8000000000000000"

	for _, traceID := range []string{kept, dropped, kept} {
		testutils.Get(t, appURL+"/items", "traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	}
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	for _, span := range spans {
		assert.Equal(t, kept, trace.TraceID(span.TraceId).String())
	}
}

func TestSampledOutRequestDropsDescendantsAndLogs(t *testing.T) {
	drop := func(sdktrace.ReadOnlySpan) (float64, bool) { return 0, true }
	for _, tc := range []struct {
		name      string
		configure func(cfg *root.Config)
	}{
		{"request stage", func(cfg *root.Config) { cfg.SampleOnRequest = drop }},
		{"response stage", func(cfg *root.Config) { cfg.SampleOnResponse = drop }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			tc.configure(cfg)
			registerForTest(t, server, cfg)
			appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, span := otel.Tracer("test").Start(r.Context(), "query")
				slog.InfoContext(ctx, "querying")
				span.End()
				writeOK(w, r)
			}))

			testutils.Get(t, appURL+"/items")
			require.NoError(t, Shutdown(context.Background()))

			assert.Empty(t, server.Spans(t))
			assert.Empty(t, server.ApplicationLogRecords(t))
		})
	}
}

func TestSampleOnRequestFailsOpen(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sampleRate    float64
		callback      func(sdktrace.ReadOnlySpan) (float64, bool)
		exportedSpans int
	}{
		{"abstain with sample rate 0", 0, func(sdktrace.ReadOnlySpan) (float64, bool) { return 1, false }, 0},
		{"abstain with sample rate 1", 1, func(sdktrace.ReadOnlySpan) (float64, bool) { return 0, false }, 1},
		{"invalid rate", 0, func(sdktrace.ReadOnlySpan) (float64, bool) { return 2, true }, 1},
		{"panic", 0, func(sdktrace.ReadOnlySpan) (float64, bool) { panic("bug") }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			cfg.SampleRate = tc.sampleRate
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

func TestSampleOnResponseDecidesOnFinalAttributes(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleOnResponse = func(span sdktrace.ReadOnlySpan) (float64, bool) {
		for _, kv := range span.Attributes() {
			if kv.Key == "http.response.status_code" && kv.Value.AsInt64() == http.StatusNotFound {
				return 0, true
			}
		}
		// The rate is ignored when abstaining, so the request-stage decision applies.
		return 0, false
	}
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", writeOK)
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/missing")
	require.NoError(t, Shutdown(context.Background()))

	span := server.SingleSpan(t)
	assert.Equal(t, "GET /items", span.Name)
}
