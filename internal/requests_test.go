package internal

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestRequestExportsServerSpanWithHandlerSpans(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("test").Start(r.Context(), "load item")
		span.End()
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items/1?q=2")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	rootSpan, child := findSpan(t, spans, "GET /items/{id}"), findSpan(t, spans, "load item")
	parsedURL, _ := url.Parse(appURL)
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, rootSpan.Kind)
	assert.Equal(t, testFramework.ScopeName, rootSpan.Scope)
	assert.Equal(t, map[string]any{
		"http.request.method":       "GET",
		"url.scheme":                "http",
		"server.address":            "127.0.0.1",
		"server.port":               int64(mustAtoi(t, parsedURL.Port())),
		"url.path":                  "/items/1",
		"url.query":                 "q=2",
		"user_agent.original":       "Go-http-client/1.1",
		"http.route":                "/items/{id}",
		"http.response.status_code": int64(200),
		"client.address":            "127.0.0.1",
	}, testutils.Attributes(rootSpan.Attributes))
	assert.Equal(t, rootSpan.SpanId, child.ParentSpanId)
	assert.Equal(t, tracepb.Span_SPAN_KIND_INTERNAL, child.Kind)
}

func TestNestedMonitoredRequestIsExportedAsSeparateRequest(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	var app http.Handler
	mux := http.NewServeMux()
	mux.HandleFunc("GET /inner", writeOK)
	mux.HandleFunc("GET /outer", func(w http.ResponseWriter, r *http.Request) {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(r.Context(), http.MethodGet, "/inner", nil))
		writeOK(w, r)
	})
	app = NetHTTPMiddleware(serveMuxRoute)(mux)
	appServer := httptest.NewServer(app)
	t.Cleanup(appServer.Close)

	testutils.Get(t, appServer.URL+"/outer")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	outer, inner := findSpan(t, spans, "GET /outer"), findSpan(t, spans, "GET /inner")
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, inner.Kind)
	assert.Equal(t, outer.SpanId, inner.ParentSpanId)
}

func TestStackedServerSpanInsideRequestIsExportedAsInternalWithWarning(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	logs := testutils.RecordSlog(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		for range 2 {
			_, span := otel.Tracer("inner-instrumentation").Start(r.Context(), "inner", trace.WithSpanKind(trace.SpanKindServer))
			span.End()
		}
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 3)
	assert.Equal(t, 1, countRequestRoots(spans))
	assert.Len(t, logs.Messages(slog.LevelWarn), 1)
}

func TestSpanEndingAfterReleaseIsDropped(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	var late trace.Span
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, late = otel.Tracer("test").Start(r.Context(), "background work")
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	late.End()
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /items", spans[0].Name)
}

func TestRequestBuffersAtMostThousandDescendantSpans(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		for range maxBufferedSpansPerRequest + 1 {
			_, span := otel.Tracer("test").Start(r.Context(), "query")
			span.End()
		}
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Len(t, server.Spans(t), maxBufferedSpansPerRequest+1)
}

func TestExcludedRequestsAreNotExportedOrSampled(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.ExcludePaths = []string{"^/internal/"}
	callbackCalls := 0
	cfg.SampleOnRequest = func(sdktrace.ReadOnlySpan) (float64, bool) {
		callbackCalls++
		return 1, true
	}
	registerForTest(t, server, cfg)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))

	testutils.Get(t, appURL+"/healthz")
	testutils.Get(t, appURL+"/Internal/stats")
	req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
	req.Header.Set("User-Agent", "kube-probe/1.30")
	testutils.Do(t, http.DefaultClient.Do, req)
	req, _ = http.NewRequest(http.MethodOptions, appURL+"/items", nil)
	testutils.Do(t, http.DefaultClient.Do, req)
	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "/items", testutils.Attributes(spans[0].Attributes)["url.path"])
	assert.Equal(t, 1, callbackCalls)
}

func findSpan(t *testing.T, spans []testutils.Span, name string) testutils.Span {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	require.Failf(t, "span not found", "no span named %q", name)
	return testutils.Span{}
}

func countRequestRoots(spans []testutils.Span) int {
	n := 0
	for _, span := range spans {
		if span.Kind == tracepb.Span_SPAN_KIND_SERVER {
			n++
		}
	}
	return n
}
