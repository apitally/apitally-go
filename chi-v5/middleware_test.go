package apitally_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	apitally "github.com/apitally/apitally-go/chi-v5"
	"github.com/apitally/apitally-go/internal"
	"github.com/apitally/apitally-go/internal/testutils"
)

func setUp(t *testing.T) *testutils.OTLPServer {
	server := testutils.NewOTLPServer(t)
	internal.SetUpTest(t)
	return server
}

func newRouter(cfg *apitally.Config, middlewares ...func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()
	apitally.Init(r, cfg)
	r.Use(middlewares...)
	r.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("item " + chi.URLParam(r, "id")))
	})
	r.Post("/items", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"token":"abc"}`))
	})
	r.Get("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		for i := range 3 {
			_, _ = fmt.Fprintf(w, "chunk %d\n", i)
			w.(http.Flusher).Flush()
		}
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/users/{userID}", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("user"))
		})
	})
	return r
}

func serve(t *testing.T, handler http.Handler) string {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(context.Background()))
}

func TestRequestExportsSingleServerSpanWithStableSemconv(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil))

	resp := testutils.Get(t, appURL+"/items/42?page=2")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	parsedURL, _ := url.Parse(appURL)
	port, _ := strconv.Atoi(parsedURL.Port())
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, spans[0].Kind)
	assert.Equal(t, "GET /items/{id}", spans[0].Name)
	assert.Equal(t, "github.com/apitally/apitally-go/chi-v5", spans[0].Scope)
	assert.Equal(t, map[string]any{
		"http.request.method":       "GET",
		"url.scheme":                "http",
		"server.address":            "127.0.0.1",
		"server.port":               int64(port),
		"url.path":                  "/items/42",
		"url.query":                 "page=2",
		"user_agent.original":       "Go-http-client/1.1",
		"http.route":                "/items/{id}",
		"http.response.status_code": int64(200),
		"client.address":            "127.0.0.1",
		"http.request.body.size":    int64(0),
		"http.response.body.size":   int64(7),
	}, testutils.Attributes(spans[0].Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil, middleware.RealIP))

	req, _ := http.NewRequest(http.MethodGet, appURL+"/items/1", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "203.0.113.7", testutils.Attributes(spans[0].Attributes)["client.address"])
}

func TestRouteIncludesGroupPrefix(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil))

	testutils.Get(t, appURL+"/api/v1/users/7")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /api/v1/users/{userID}", spans[0].Name)
	assert.Equal(t, "/api/v1/users/{userID}", testutils.Attributes(spans[0].Attributes)["http.route"])
}

func TestFirstRequestActivatesAndIsRecorded(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
	records := server.LogRecords(t)
	require.Len(t, records, 1)
	assert.Equal(t, "apitally.app.startup", records[0].EventName)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	records := server.LogRecords(t)
	require.Len(t, records, 1)
	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(records[0].Body.GetStringValue()), &body))
	assert.Equal(t, "chi", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/items/{id}"},
		{"method": "POST", "path": "/items"},
		{"method": "GET", "path": "/stream"},
		{"method": "GET", "path": "/api/v1/users/{userID}"},
	}, body.Paths)
}

func TestRequestAndResponseBodiesCapturedAndRedacted(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureRequestBody = true
	cfg.CaptureResponseBody = true
	appURL := serve(t, newRouter(cfg))

	req, _ := http.NewRequest(http.MethodPost, appURL+"/items", strings.NewReader(`{"name": "x", "password": "secret"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	assert.Equal(t, `{"id":1,"token":"abc"}`, resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, `{"name":"x","password":"[REDACTED]"}`, attrs["apitally.request.body"])
	assert.Equal(t, `{"id":1,"token":"[REDACTED]"}`, attrs["apitally.response.body"])
	assert.Equal(t, int64(35), attrs["http.request.body.size"])
	assert.Equal(t, int64(22), attrs["http.response.body.size"])
}

func TestStreamingResponseSizeAndBodyCaptured(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureResponseBody = true
	appURL := serve(t, newRouter(cfg))

	resp := testutils.Get(t, appURL+"/stream")
	shutDown(t)

	assert.Equal(t, "chunk 0\nchunk 1\nchunk 2\n", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, "chunk 0\nchunk 1\nchunk 2\n", attrs["apitally.response.body"])
	assert.Equal(t, int64(24), attrs["http.response.body.size"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.response.body.size")
	require.Len(t, points, 1)
	assert.Equal(t, 24.0, points[0].GetSum())
}

func TestUnmatchedRequestHasNoRouteAndNoHistogramPoint(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newRouter(nil))

	resp := testutils.Get(t, appURL+"/missing")
	shutDown(t)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET", spans[0].Name)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.NotContains(t, attrs, "http.route")
	assert.Equal(t, int64(404), attrs["http.response.status_code"])
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
}

func TestPreInstrumentedAppAdaptsWithoutDuplicateSpans(t *testing.T) {
	server := setUp(t)
	userSpans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(userSpans)))
	appURL := serve(t, testutils.InstrumentHTTPHandler(newRouter(nil)))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	require.Len(t, userSpans.GetSpans(), 1)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, userSpans.GetSpans()[0].SpanContext.SpanID(), trace.SpanID(spans[0].SpanId))
	assert.Equal(t, "/items/{id}", testutils.Attributes(spans[0].Attributes)["http.route"])
}

func TestInitTwiceDoesNotStackMiddleware(t *testing.T) {
	server := setUp(t)
	r := chi.NewRouter()
	apitally.Init(r, nil)
	apitally.Init(r, nil)
	r.Get("/items", func(w http.ResponseWriter, r *http.Request) {})
	appURL := serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	appURL := serve(t, newRouter(cfg))

	item := testutils.Get(t, appURL+"/items/1")
	missing := testutils.Get(t, appURL+"/missing")
	shutDown(t)

	assert.Equal(t, http.StatusOK, item.StatusCode)
	assert.Equal(t, "item 1", item.Body)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Empty(t, server.Requests())
}
