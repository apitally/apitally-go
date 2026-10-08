package apitally_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	apitally "github.com/apitally/apitally-go/echo-v4"
	"github.com/apitally/apitally-go/internal"
	"github.com/apitally/apitally-go/internal/testutils"
)

func setUp(t *testing.T) *testutils.OTLPServer {
	server := testutils.NewOTLPServer(t)
	internal.SetUpTest(t)
	testutils.SetSlogDefault(t, apitally.NewSlogHandler(slog.NewTextHandler(io.Discard, nil)))
	return server
}

var validate = validator.New()

type item struct {
	Name  string `validate:"required"`
	Price int    `validate:"gte=10"`
}

func newApp(cfg *apitally.Config) *echo.Echo {
	e := echo.New()
	e.Use(middleware.Recover())
	apitally.Init(e, cfg)
	e.GET("/items/:id", func(c echo.Context) error {
		slog.InfoContext(c.Request().Context(), "fetching item", "id", c.Param("id"))
		return c.String(http.StatusOK, "item "+c.Param("id"))
	})
	e.POST("/items", func(c echo.Context) error {
		_, _ = io.ReadAll(c.Request().Body)
		return c.Blob(http.StatusCreated, "application/json", []byte(`{"id":1,"token":"abc"}`))
	})
	e.GET("/stream", func(c echo.Context) error {
		c.Response().Header().Set("Content-Type", "text/plain")
		for i := range 3 {
			_, _ = fmt.Fprintf(c.Response(), "chunk %d\n", i)
			c.Response().Flush()
		}
		return nil
	})
	e.GET("/panic", func(c echo.Context) error {
		panic(errors.New("boom"))
	})
	e.POST("/validate", func(c echo.Context) error {
		if err := validate.Struct(item{Name: "", Price: 5}); err != nil {
			return echo.NewHTTPError(http.StatusUnprocessableEntity).SetInternal(err)
		}
		return nil
	})
	e.GET("/error", func(c echo.Context) error {
		return errors.New("failed")
	})
	g := e.Group("/api/v1")
	g.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if id := c.Request().Header.Get("X-Consumer"); id != "" {
				apitally.SetConsumer(c.Request().Context(), apitally.Consumer{Identifier: id, Name: "Acme Corp"})
			}
			return next(c)
		}
	})
	g.GET("", func(c echo.Context) error {
		return c.String(http.StatusOK, "api")
	})
	g.GET("/users/:userID", func(c echo.Context) error {
		return c.String(http.StatusOK, "user")
	})
	return e
}

func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(context.Background()))
}

func TestRequestExportsSingleServerSpanWithStableSemconv(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

	resp := testutils.Get(t, appURL+"/items/42?page=2")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	parsedURL, _ := url.Parse(appURL)
	port, _ := strconv.Atoi(parsedURL.Port())
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, spans[0].Kind)
	assert.Equal(t, "GET /items/:id", spans[0].Name)
	assert.Equal(t, "github.com/apitally/apitally-go/echo-v4", spans[0].Scope)
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
		"http.response.header.content-type": []any{"text/plain; charset=UTF-8"},
		"client.address":                    "127.0.0.1",
		"http.request.body.size":            int64(0),
		"http.response.body.size":           int64(7),
	}, testutils.Attributes(spans[0].Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	e := newApp(nil)
	e.IPExtractor = echo.ExtractIPFromXFFHeader()
	appURL := testutils.Serve(t, e)

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
	appURL := testutils.Serve(t, newApp(nil))

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
	logs := server.ApplicationLogRecords(t)
	require.Len(t, logs, 1)
	assert.Equal(t, "fetching item", logs[0].Body.GetStringValue())
	assert.Equal(t, spans[0].TraceId, logs[0].TraceId)
	assert.Equal(t, trace.SpanID(spans[0].SpanId).String(), testutils.Attributes(logs[0].Attributes)["apitally.request.server_span_id"])
}

func TestRouteIncludesGroupPrefix(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

	testutils.Get(t, appURL+"/api/v1/users/7")
	testutils.Get(t, appURL+"/api/v1")
	shutDown(t)

	var routes []any
	for _, span := range server.Spans(t) {
		routes = append(routes, testutils.Attributes(span.Attributes)["http.route"])
	}
	assert.ElementsMatch(t, []any{"/api/v1/users/:userID", "/api/v1"}, routes)
}

func TestFirstRequestActivatesAndIsRecorded(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
	assert.Len(t, server.Events(t, "apitally.app.startup"), 1)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	server.DecodeStartupEvent(t, &body)
	assert.Equal(t, "echo", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/items/:id"},
		{"method": "POST", "path": "/items"},
		{"method": "GET", "path": "/stream"},
		{"method": "GET", "path": "/panic"},
		{"method": "POST", "path": "/validate"},
		{"method": "GET", "path": "/error"},
		{"method": "GET", "path": "/api/v1"},
		{"method": "GET", "path": "/api/v1/users/:userID"},
	}, body.Paths)
}

func TestRequestAndResponseBodiesCapturedAndRedacted(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureRequestBody = true
	cfg.CaptureResponseBody = true
	appURL := testutils.Serve(t, newApp(cfg))

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
	appURL := testutils.Serve(t, newApp(cfg))

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
	appURL := testutils.Serve(t, newApp(nil))

	missing := testutils.Get(t, appURL+"/api/v1/missing")
	req, _ := http.NewRequest(http.MethodDelete, appURL+"/items/1", nil)
	notAllowed := testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusMethodNotAllowed, notAllowed.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	statusCodes := map[string]any{}
	for _, span := range spans {
		attrs := testutils.Attributes(span.Attributes)
		assert.NotContains(t, attrs, "http.route")
		statusCodes[span.Name] = attrs["http.response.status_code"]
	}
	assert.Equal(t, map[string]any{"GET": int64(404), "DELETE": int64(405)}, statusCodes)
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
}

func TestSetConsumerReachesSpanAndHistogram(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

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
	appURL := testutils.Serve(t, newApp(nil))

	resp := testutils.Get(t, appURL+"/panic")
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	attrs := testutils.Attributes(spans[0].Events[0].Attributes)
	assert.Equal(t, "errors.errorString", attrs["exception.type"])
	assert.Equal(t, "boom", attrs["exception.message"])
	assert.Regexp(t, `^github.com/apitally/apitally-go/echo-v4_test.newApp.func\d+\n\t\S+/middleware_test.go:\d+\n`, attrs["exception.stacktrace"])
	assert.Equal(t, int64(500), testutils.Attributes(spans[0].Attributes)["http.response.status_code"])
}

func TestValidationErrorReported(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newApp(nil))

	req, _ := http.NewRequest(http.MethodPost, appURL+"/validate", nil)
	resp := testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
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
	appURL := testutils.Serve(t, testutils.InstrumentHTTPHandler(newApp(nil)))

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
	e := echo.New()
	apitally.Init(e, nil)
	apitally.Init(e, nil)
	e.GET("/items", func(c echo.Context) error { return nil })
	appURL := testutils.Serve(t, e)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	appURL := testutils.Serve(t, newApp(cfg))

	item := testutils.Get(t, appURL+"/items/1")
	missing := testutils.Get(t, appURL+"/missing")
	failed := testutils.Get(t, appURL+"/error")
	shutDown(t)

	assert.Equal(t, http.StatusOK, item.StatusCode)
	assert.Equal(t, "item 1", item.Body)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, failed.StatusCode)
	assert.Equal(t, `{"message":"Internal Server Error"}`+"\n", failed.Body)
	assert.Empty(t, server.Requests())
}

func TestHostRouterRequestHasRoute(t *testing.T) {
	server := setUp(t)
	e := echo.New()
	apitally.Init(e, nil)
	e.Host("api.example.com").GET("/orders/:id", func(c echo.Context) error {
		return c.String(http.StatusOK, "order")
	})
	appURL := testutils.Serve(t, e)

	req, err := http.NewRequest(http.MethodGet, appURL+"/orders/1", nil)
	require.NoError(t, err)
	req.Host = "api.example.com"
	resp := testutils.Do(t, http.DefaultClient.Do, req)
	shutDown(t)

	assert.Equal(t, "order", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /orders/:id", spans[0].Name)
	assert.Equal(t, "/orders/:id", testutils.Attributes(spans[0].Attributes)["http.route"])
}

func TestReturnedErrorIsDispatchedToErrorHandlerOnce(t *testing.T) {
	server := setUp(t)
	e := newApp(nil)
	errorHandlerCalls := 0
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		errorHandlerCalls++
		_ = c.String(http.StatusInternalServerError, "custom error")
	}
	appURL := testutils.Serve(t, e)

	resp := testutils.Get(t, appURL+"/error")
	shutDown(t)

	assert.Equal(t, 1, errorHandlerCalls)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "custom error", resp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, int64(500), testutils.Attributes(spans[0].Attributes)["http.response.status_code"])
	serverErrors := server.Events(t, "apitally.request.server_error")
	require.Len(t, serverErrors, 1)
	assert.Equal(t, map[string]any{
		"method": "GET", "path": "/error", "type": "errors.errorString", "message": "failed", "stacktrace": "",
		"counts": []any{map[string]any{"count": int64(1)}},
	}, testutils.Value(serverErrors[0].Body))
}
