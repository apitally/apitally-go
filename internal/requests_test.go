package internal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	assert.Equal(t, rootSpan.SpanId, child.ParentSpanId)
}

func TestRequestAttributesAreSetOnServerSpan(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRequestAttributes(r.Context(), attribute.String("tenant", "acme"), attribute.StringSlice("roles", []string{"admin", "billing"}))
		writeOK(w, r)
	}))

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, "acme", attrs["tenant"])
	assert.Equal(t, []any{"admin", "billing"}, attrs["roles"])
}

func TestNestedRequestIsExportedSeparately(t *testing.T) {
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
	appURL := testutils.Serve(t, app)

	testutils.Get(t, appURL+"/outer")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	outer, inner := findSpan(t, spans, "GET /outer"), findSpan(t, spans, "GET /inner")
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, inner.Kind)
	assert.Equal(t, outer.SpanId, inner.ParentSpanId)
}

func TestStackedServerSpanIsExportedAsInternal(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	logs := testutils.RecordSlog(t)
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
	kinds := map[string][]tracepb.Span_SpanKind{}
	for _, span := range spans {
		kinds[span.Name] = append(kinds[span.Name], span.Kind)
	}
	assert.Equal(t, map[string][]tracepb.Span_SpanKind{
		"GET /items": {tracepb.Span_SPAN_KIND_SERVER},
		"inner":      {tracepb.Span_SPAN_KIND_INTERNAL, tracepb.Span_SPAN_KIND_INTERNAL},
	}, kinds)
	assert.Equal(t, []string{"Instrumentation scope inner-instrumentation starts SERVER spans inside Apitally's request span. Apitally exports them as INTERNAL spans, but other exporters receive two SERVER spans per request. Install that instrumentation outside Apitally's middleware."}, logs.Messages(slog.LevelWarn))
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
	testutils.Get(t, appURL+"/items", "User-Agent", "kube-probe/1.30")
	req, _ := http.NewRequest(http.MethodOptions, appURL+"/items", nil)
	testutils.Do(t, http.DefaultClient.Do, req)
	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "/items", testutils.Attributes(spans[0].Attributes)["url.path"])
	assert.Equal(t, 1, callbackCalls)
}

func TestWebSocketUpgradeIsNotMonitored(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nhello")
		_ = buffer.Flush()
		_ = conn.Close()
		// A panic with a captured error would otherwise produce a server error event.
		panic(errors.New("connection lost"))
	})
	app := NetHTTPMiddleware(serveMuxRoute)(mux)
	// The client reads the hijacked response before the middleware ends the
	// request, so the test waits for the request to be served.
	served := make(chan any, 1)
	appURL := testutils.Serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { served <- recover() }()
		app.ServeHTTP(w, r)
	}))

	resp := testutils.Get(t, appURL+"/ws", "Connection", "Upgrade", "Upgrade", "websocket")
	<-served
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	assert.Equal(t, "websocket", resp.Header.Get("Upgrade"))
	assert.Equal(t, "hello", resp.Body)
	assert.Empty(t, server.Spans(t))
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
	assert.Empty(t, server.Events(t, serverErrorEventName))
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
