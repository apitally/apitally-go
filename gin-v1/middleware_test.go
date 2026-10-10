package apitally_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

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
			// gin.ResponseWriter writes through WriteString as well as Write.
			_, _ = c.Writer.WriteString(fmt.Sprintf("chunk %d\n", i))
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
	api.GET("", func(c *gin.Context) {
		c.String(http.StatusOK, "api")
	})
	api.GET("/users/:userID", func(c *gin.Context) {
		c.String(http.StatusOK, "user")
	})
	return r
}

func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(t.Context()))
}

func TestRequestExportsSingleServerSpanWithStableSemconv(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	resp := testutils.Get(t, appURL+"/items/42?page=2")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	span := server.SingleSpan(t)
	parsedURL, _ := url.Parse(appURL)
	port, _ := strconv.Atoi(parsedURL.Port())
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, span.Kind)
	assert.Equal(t, "GET /items/:id", span.Name)
	assert.Equal(t, "github.com/apitally/apitally-go/gin-v1", span.Scope)
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
	}, testutils.Attributes(span.Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	r := newEngine(nil)
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items/1", "X-Forwarded-For", "203.0.113.7")
	shutDown(t)

	span := server.SingleSpan(t)
	assert.Equal(t, "203.0.113.7", testutils.Attributes(span.Attributes)["client.address"])
}

func TestHistogramAttributesAndLogCorrelation(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/items/42")
	shutDown(t)

	span := server.SingleSpan(t)
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, uint64(1), points[0].Count)
	assert.Equal(t, "/items/:id", testutils.Attributes(points[0].Attributes)["http.route"])
	logs := server.ApplicationLogRecords(t)
	require.Len(t, logs, 1)
	assert.Equal(t, "fetching item", logs[0].Body.GetStringValue())
	assert.Equal(t, span.TraceId, logs[0].TraceId)
	assert.Equal(t, trace.SpanID(span.SpanId).String(), testutils.Attributes(logs[0].Attributes)["apitally.request.server_span_id"])
}

func TestRouteIncludesGroupPrefix(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/api/v1/users/7")
	testutils.Get(t, appURL+"/api/v1")
	shutDown(t)

	var routes []any
	for _, span := range server.Spans(t) {
		routes = append(routes, testutils.Attributes(span.Attributes)["http.route"])
	}
	assert.ElementsMatch(t, []any{"/api/v1/users/:userID", "/api/v1"}, routes)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	server.DecodeStartupEvent(t, &body)
	assert.Equal(t, "gin", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/items/:id"},
		{"method": "POST", "path": "/items"},
		{"method": "GET", "path": "/stream"},
		{"method": "GET", "path": "/panic"},
		{"method": "POST", "path": "/validate"},
		{"method": "GET", "path": "/api/v1"},
		{"method": "GET", "path": "/api/v1/users/:userID"},
	}, body.Paths)
}

func TestRequestAndResponseBodiesCapturedAndRedacted(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureRequestBody = true
	cfg.CaptureResponseBody = true
	appURL := testutils.Serve(t, newEngine(cfg))

	resp := testutils.Send(t, http.MethodPost, appURL+"/items", `{"name": "x", "password": "secret"}`, "Content-Type", "application/json")
	shutDown(t)

	assert.Equal(t, `{"id":1,"token":"abc"}`, resp.Body)
	span := server.SingleSpan(t)
	attrs := testutils.Attributes(span.Attributes)
	assert.Equal(t, `{"name":"x","password":"[REDACTED]"}`, attrs["apitally.request.body"])
	assert.Equal(t, `{"id":1,"token":"[REDACTED]"}`, attrs["apitally.response.body"])
	assert.Equal(t, int64(35), attrs["http.request.body.size"])
	assert.Equal(t, int64(22), attrs["http.response.body.size"])
}

func TestStreamingResponseSizeAndBodyCaptured(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureResponseBody = true
	appURL := testutils.Serve(t, newEngine(cfg))

	resp := testutils.Get(t, appURL+"/stream")
	shutDown(t)

	assert.Equal(t, "chunk 0\nchunk 1\nchunk 2\n", resp.Body)
	span := server.SingleSpan(t)
	attrs := testutils.Attributes(span.Attributes)
	assert.Equal(t, "chunk 0\nchunk 1\nchunk 2\n", attrs["apitally.response.body"])
	assert.Equal(t, int64(24), attrs["http.response.body.size"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.response.body.size")
	require.Len(t, points, 1)
	assert.Equal(t, 24.0, points[0].GetSum())
}

func TestUnmatchedRequestHasNoRouteAndNoHistogramPoint(t *testing.T) {
	server := setUp(t)
	r := newEngine(nil)
	r.HandleMethodNotAllowed = true
	appURL := testutils.Serve(t, r)

	missing := testutils.Get(t, appURL+"/missing")
	notAllowed := testutils.Send(t, http.MethodDelete, appURL+"/items/1", "")
	shutDown(t)

	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, "404 page not found", missing.Body)
	assert.Equal(t, http.StatusMethodNotAllowed, notAllowed.StatusCode)
	assert.Equal(t, "405 method not allowed", notAllowed.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	responses := map[string][]any{}
	for _, span := range spans {
		attrs := testutils.Attributes(span.Attributes)
		assert.NotContains(t, attrs, "http.route")
		responses[span.Name] = []any{attrs["http.response.status_code"], attrs["http.response.header.content-type"], attrs["http.response.body.size"]}
	}
	assert.Equal(t, map[string][]any{
		"GET":    {int64(404), []any{"text/plain"}, int64(18)},
		"DELETE": {int64(405), []any{"text/plain"}, int64(22)},
	}, responses)
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
}

func TestSetConsumerReachesSpanAndHistogram(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	testutils.Get(t, appURL+"/api/v1/users/7", "X-Consumer", "acme")
	shutDown(t)

	span := server.SingleSpan(t)
	assert.Equal(t, "acme", testutils.Attributes(span.Attributes)["apitally.consumer.identifier"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, "acme", testutils.Attributes(points[0].Attributes)["apitally.consumer.identifier"])
}

func TestUnhandledPanicRecordedOnServerSpan(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	resp := testutils.Get(t, appURL+"/panic")
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	span := server.SingleSpan(t)
	require.Len(t, span.Events, 1)
	attrs := testutils.Attributes(span.Events[0].Attributes)
	assert.Equal(t, "boom", attrs["exception.message"])
	assert.Regexp(t, `^github.com/apitally/apitally-go/gin-v1_test.newEngine.func\d+\n\t\S+/middleware_test.go:\d+\n`, attrs["exception.stacktrace"])
	assert.Equal(t, int64(500), testutils.Attributes(span.Attributes)["http.response.status_code"])
}

func TestValidationErrorReported(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newEngine(nil))

	resp := testutils.Send(t, http.MethodPost, appURL+"/validate", `{"name": "", "price": 5}`, "Content-Type", "application/json")
	shutDown(t)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var validationErrors [][3]any
	for _, record := range server.Events(t, "apitally.request.validation_error") {
		body := testutils.Value(record.Body).(map[string]any)
		validationErrors = append(validationErrors, [3]any{body["field"], body["type"], body["counts"]})
	}
	counts := []any{map[string]any{"count": int64(1)}}
	assert.ElementsMatch(t, [][3]any{{"Name", "required", counts}, {"Price", "gte", counts}}, validationErrors)
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
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	require.Len(t, userSpans.GetSpans(), 1)
	span := server.SingleSpan(t)
	assert.Equal(t, userSpans.GetSpans()[0].SpanContext.SpanID(), trace.SpanID(span.SpanId))
	assert.Equal(t, "/items/:id", testutils.Attributes(span.Attributes)["http.route"])
}

func TestInitTwiceDoesNotStackMiddleware(t *testing.T) {
	server := setUp(t)
	r := gin.New()
	apitally.Init(r, nil)
	apitally.Init(r, nil)
	r.GET("/items", func(c *gin.Context) {})
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	appURL := testutils.Serve(t, newEngine(cfg))

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

func TestAbortWithErrorIsRecordedAsServerError(t *testing.T) {
	server := setUp(t)
	r := gin.New()
	apitally.Init(r, nil)
	r.GET("/error", func(c *gin.Context) {
		_ = c.AbortWithError(http.StatusInternalServerError, errors.New("failed"))
	})
	appURL := testutils.Serve(t, r)

	resp := testutils.Get(t, appURL+"/error")
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	span := server.SingleSpan(t)
	require.Len(t, span.Events, 1)
	assert.Equal(t, "failed", testutils.Attributes(span.Events[0].Attributes)["exception.message"])
	assert.Len(t, server.Events(t, "apitally.request.server_error"), 1)
}

func TestResponseControllerReachesUnderlyingWriter(t *testing.T) {
	setUp(t)
	r := gin.New()
	apitally.Init(r, nil)
	r.GET("/deadline", func(c *gin.Context) {
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
		}
	})
	appURL := testutils.Serve(t, r)

	resp := testutils.Get(t, appURL+"/deadline")
	shutDown(t)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Body)
}

func TestInitAfterRoutesLogsError(t *testing.T) {
	server := setUp(t)
	logs := testutils.RecordSlog(t)
	r := gin.New()
	r.GET("/early", func(c *gin.Context) {})
	apitally.Init(r, nil)
	r.GET("/late", func(c *gin.Context) {})
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/early")
	testutils.Get(t, appURL+"/late")
	shutDown(t)

	assert.Len(t, logs.Messages(slog.LevelError), 1)
	span := server.SingleSpan(t)
	assert.Equal(t, "GET /late", span.Name)
}
