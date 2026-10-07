package apitally_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	apitally "github.com/apitally/apitally-go/gin-v1"
	"github.com/apitally/apitally-go/internal"
	"github.com/apitally/apitally-go/internal/testutils"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setUp(t *testing.T) *testutils.OTLPServer {
	server := testutils.NewOTLPServer(t)
	internal.SetUpTest(t)
	testutils.SetSlogDefault(t, apitally.NewSlogHandler(slog.NewTextHandler(io.Discard, nil)))
	return server
}

type item struct {
	Name  string `json:"name" binding:"required"`
	Price int    `json:"price" binding:"gte=10"`
}

func newEngine(cfg *apitally.Config) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	apitally.Init(r, cfg)
	r.GET("/items/:id", func(c *gin.Context) {
		slog.InfoContext(c, "fetching item", "id", c.Param("id"))
		c.String(http.StatusOK, "item "+c.Param("id"))
	})
	r.POST("/items", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		c.Data(http.StatusCreated, "application/json", []byte(`{"id":1,"token":"abc"}`))
	})
	r.GET("/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		for i := range 3 {
			_, _ = fmt.Fprintf(c.Writer, "chunk %d\n", i)
			c.Writer.Flush()
		}
	})
	r.GET("/panic", func(c *gin.Context) {
		panic(errors.New("boom"))
	})
	r.POST("/validate", func(c *gin.Context) {
		var body item
		if c.Bind(&body) == nil {
			c.Status(http.StatusNoContent)
		}
	})
	api := r.Group("/api/v1", func(c *gin.Context) {
		if id := c.GetHeader("X-Consumer"); id != "" {
			apitally.SetConsumer(c, apitally.Consumer{Identifier: id, Name: "Acme Corp"})
		}
	})
	api.GET("/users/:userID", func(c *gin.Context) {
		c.String(http.StatusOK, "user")
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
	appURL := serve(t, newEngine(nil))

	resp := testutils.Get(t, appURL+"/items/42?page=2")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	parsedURL, _ := url.Parse(appURL)
	port, _ := strconv.Atoi(parsedURL.Port())
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, spans[0].Kind)
	assert.Equal(t, "GET /items/:id", spans[0].Name)
	assert.Equal(t, "github.com/apitally/apitally-go/gin-v1", spans[0].Scope)
	assert.Equal(t, map[string]any{
		"http.request.method":               "GET",
		"url.scheme":                        "http",
		"server.address":                    "127.0.0.1",
		"server.port":                       int64(port),
		"url.path":                          "/items/42",
		"url.query":                         "page=2",
		"user_agent.original":               "Go-http-client/1.1",
		"http.route":                        "/items/:id",
		"http.response.status_code":         int64(200),
		"client.address":                    "127.0.0.1",
		"http.request.body.size":            int64(0),
		"http.response.body.size":           int64(7),
		"http.response.header.content-type": []any{"text/plain; charset=utf-8"},
	}, testutils.Attributes(spans[0].Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	r := newEngine(nil)
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	appURL := serve(t, r)

	req, _ := http.NewRequest(http.MethodGet, appURL+"/items/1", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "203.0.113.7", testutils.Attributes(spans[0].Attributes)["client.address"])
}

func TestHistogramAttributesAndLogCorrelation(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/items/42")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, uint64(1), points[0].Count)
	assert.Equal(t, map[string]any{
		"http.request.method":       "GET",
		"http.route":                "/items/:id",
		"http.response.status_code": int64(200),
		"url.scheme":                "http",
	}, testutils.Attributes(points[0].Attributes))
	var logs []testutils.LogRecord
	for _, record := range server.LogRecords(t) {
		if record.Scope == "slog" {
			logs = append(logs, record)
		}
	}
	require.Len(t, logs, 1)
	assert.Equal(t, "fetching item", logs[0].Body.GetStringValue())
	assert.Equal(t, spans[0].TraceId, logs[0].TraceId)
	assert.Equal(t, trace.SpanID(spans[0].SpanId).String(), testutils.Attributes(logs[0].Attributes)["apitally.request.server_span_id"])
}

func TestRouteIncludesGroupPrefix(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/api/v1/users/7")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /api/v1/users/:userID", spans[0].Name)
	assert.Equal(t, "/api/v1/users/:userID", testutils.Attributes(spans[0].Attributes)["http.route"])
}

func TestFirstRequestActivatesAndIsRecorded(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
	assert.Len(t, server.Events(t, "apitally.app.startup"), 1)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	records := server.Events(t, "apitally.app.startup")
	require.Len(t, records, 1)
	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(records[0].Body.GetStringValue()), &body))
	assert.Equal(t, "gin", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/items/:id"},
		{"method": "POST", "path": "/items"},
		{"method": "GET", "path": "/stream"},
		{"method": "GET", "path": "/panic"},
		{"method": "POST", "path": "/validate"},
		{"method": "GET", "path": "/api/v1/users/:userID"},
	}, body.Paths)
}

func TestRequestAndResponseBodiesCapturedAndRedacted(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureRequestBody = true
	cfg.CaptureResponseBody = true
	appURL := serve(t, newEngine(cfg))

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
	appURL := serve(t, newEngine(cfg))

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
	appURL := serve(t, newEngine(nil))

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

func TestSetConsumerReachesSpanAndHistogram(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	req, _ := http.NewRequest(http.MethodGet, appURL+"/api/v1/users/7", nil)
	req.Header.Set("X-Consumer", "acme")
	testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "acme", testutils.Attributes(spans[0].Attributes)["apitally.consumer.identifier"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, "acme", testutils.Attributes(points[0].Attributes)["apitally.consumer.identifier"])
	updates := server.Events(t, "apitally.consumer.update")
	require.Len(t, updates, 1)
	assert.Equal(t, map[string]any{"identifier": "acme", "name": "Acme Corp"}, testutils.Value(updates[0].Body))
}

func TestUnhandledPanicRecordedOnServerSpan(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	resp := testutils.Get(t, appURL+"/panic")
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	attrs := testutils.Attributes(spans[0].Events[0].Attributes)
	assert.Equal(t, "errors.errorString", attrs["exception.type"])
	assert.Equal(t, "boom", attrs["exception.message"])
	assert.Regexp(t, `^github.com/apitally/apitally-go/gin-v1_test.newEngine.func\d+\n\t\S+/middleware_test.go:\d+\n`, attrs["exception.stacktrace"])
	assert.Equal(t, int64(500), testutils.Attributes(spans[0].Attributes)["http.response.status_code"])
	assert.Len(t, server.Events(t, "apitally.request.server_error"), 1)
}

func TestValidationErrorReported(t *testing.T) {
	server := setUp(t)
	appURL := serve(t, newEngine(nil))

	req, _ := http.NewRequest(http.MethodPost, appURL+"/validate", strings.NewReader(`{"name": "", "price": 5}`))
	req.Header.Set("Content-Type", "application/json")
	resp := testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var events []any
	for _, record := range server.Events(t, "apitally.request.validation_error") {
		events = append(events, testutils.Value(record.Body))
	}
	event := func(field, tag, message string) map[string]any {
		return map[string]any{
			"method": "POST", "path": "/validate", "source": "", "field": field, "type": tag, "message": message,
			"counts": []any{map[string]any{"count": int64(1)}},
		}
	}
	assert.ElementsMatch(t, []any{
		event("Name", "required", "Key: 'item.Name' Error:Field validation for 'Name' failed on the 'required' tag"),
		event("Price", "gte", "Key: 'item.Price' Error:Field validation for 'Price' failed on the 'gte' tag"),
	}, events)
}

func TestPreInstrumentedAppAdaptsWithoutDuplicateSpans(t *testing.T) {
	server := setUp(t)
	userSpans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(userSpans)))
	r := gin.New()
	// Registered before Init, as otelgin is.
	r.Use(func(c *gin.Context) {
		testutils.InstrumentHTTPHandler(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			c.Request = req
			c.Next()
		})).ServeHTTP(c.Writer, c.Request)
	})
	apitally.Init(r, nil)
	r.GET("/items/:id", func(c *gin.Context) { c.String(http.StatusOK, "item") })
	appURL := serve(t, r)

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	require.Len(t, userSpans.GetSpans(), 1)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, userSpans.GetSpans()[0].SpanContext.SpanID(), trace.SpanID(spans[0].SpanId))
	assert.Equal(t, "/items/:id", testutils.Attributes(spans[0].Attributes)["http.route"])
}

func TestInitTwiceDoesNotStackMiddleware(t *testing.T) {
	server := setUp(t)
	r := gin.New()
	apitally.Init(r, nil)
	apitally.Init(r, nil)
	r.GET("/items", func(c *gin.Context) {})
	appURL := serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	appURL := serve(t, newEngine(cfg))

	item := testutils.Get(t, appURL+"/items/1")
	missing := testutils.Get(t, appURL+"/missing")
	panicked := testutils.Get(t, appURL+"/panic")
	shutDown(t)

	assert.Equal(t, http.StatusOK, item.StatusCode)
	assert.Equal(t, "item 1", item.Body)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, panicked.StatusCode)
	assert.Empty(t, server.Requests())
}

func TestWriteStringIsCountedAndCapturedOnce(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureResponseBody = true
	r := gin.New()
	apitally.Init(r, cfg)
	r.GET("/text", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		_, _ = c.Writer.WriteString("hello ")
		_, _ = c.Writer.Write([]byte("world"))
	})
	appURL := serve(t, r)

	resp := testutils.Get(t, appURL+"/text")
	shutDown(t)

	assert.Equal(t, "hello world", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, "hello world", attrs["apitally.response.body"])
	assert.Equal(t, int64(11), attrs["http.response.body.size"])
}

func TestInitAfterRoutesLogsErrorAndMonitorsLaterRoutes(t *testing.T) {
	server := setUp(t)
	logs := testutils.RecordSlog(t)
	r := gin.New()
	r.GET("/early", func(c *gin.Context) {})
	apitally.Init(r, nil)
	r.GET("/late", func(c *gin.Context) {})
	appURL := serve(t, r)

	testutils.Get(t, appURL+"/early")
	testutils.Get(t, appURL+"/late")
	shutDown(t)

	assert.Len(t, logs.Messages(slog.LevelError), 1)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /late", spans[0].Name)
}
