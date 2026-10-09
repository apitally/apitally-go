package apitally_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	apitally "github.com/apitally/apitally-go/fiber-v2"
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

func newApp(cfg *apitally.Config, config ...fiber.Config) *fiber.App {
	app := fiber.New(config...)
	app.Use(recover.New())
	apitally.Init(app, cfg)
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("bookstore")
	})
	app.Get("/items/:id", func(c *fiber.Ctx) error {
		slog.InfoContext(c.UserContext(), "fetching item", "id", c.Params("id"))
		return c.SendString("item " + c.Params("id"))
	})
	app.Post("/items", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Status(fiber.StatusCreated).SendString(`{"id":1,"token":"abc"}`)
	})
	app.Get("/stream", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlain)
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			for i := range 3 {
				_, _ = fmt.Fprintf(w, "chunk %d\n", i)
				_ = w.Flush()
			}
		})
		return nil
	})
	app.Get("/panic", func(c *fiber.Ctx) error {
		panic(errors.New("boom"))
	})
	app.Post("/validate", func(c *fiber.Ctx) error {
		if err := validate.Struct(item{Name: "", Price: 5}); err != nil {
			return fmt.Errorf("%w: %w", fiber.ErrUnprocessableEntity, err)
		}
		return nil
	})
	app.Get("/error", func(c *fiber.Ctx) error {
		return errors.New("failed")
	})
	api := app.Group("/api/v1", func(c *fiber.Ctx) error {
		if id := c.Get("X-Consumer"); id != "" {
			apitally.SetConsumer(c.UserContext(), apitally.Consumer{Identifier: id, Name: "Acme Corp"})
		}
		return c.Next()
	})
	api.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("api")
	})
	api.Get("/users/:userID", func(c *fiber.Ctx) error {
		return c.SendString("user")
	})
	return app
}

// send sends a request with app.Test. A path is sent to host example.com,
// because fasthttp requires a Host header.
func send(t *testing.T, app *fiber.App, method, url string, body io.Reader, headers ...string) testutils.Response {
	if strings.HasPrefix(url, "/") {
		url = "http://example.com" + url
	}
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return testutils.Do(t, func(req *http.Request) (*http.Response, error) { return app.Test(req, -1) }, req)
}

func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(t.Context()))
}

func TestRequestExportsSingleServerSpanWithStableSemconv(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	resp := send(t, app, http.MethodGet, "/items/42?page=2", nil)
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	span := server.SingleSpan(t)
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, span.Kind)
	assert.Equal(t, "GET /items/:id", span.Name)
	assert.Equal(t, "github.com/apitally/apitally-go/fiber-v2", span.Scope)
	assert.Equal(t, map[string]any{
		"http.request.method":               "GET",
		"url.scheme":                        "http",
		"server.address":                    "example.com",
		"url.path":                          "/items/42",
		"url.query":                         "page=2",
		"http.route":                        "/items/:id",
		"http.response.status_code":         int64(200),
		"client.address":                    "0.0.0.0",
		"http.request.body.size":            int64(0),
		"http.response.body.size":           int64(7),
		"http.response.header.content-type": []any{"text/plain; charset=utf-8"},
	}, testutils.Attributes(span.Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	app := newApp(nil, fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor})

	send(t, app, http.MethodGet, "/items/1", nil, "X-Forwarded-For", "203.0.113.7")
	shutDown(t)

	span := server.SingleSpan(t)
	assert.Equal(t, "203.0.113.7", testutils.Attributes(span.Attributes)["client.address"])
}

func TestHistogramAttributesAndLogCorrelation(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	send(t, app, http.MethodGet, "/items/42", nil)
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
	app := newApp(nil)

	send(t, app, http.MethodGet, "/api/v1/users/7", nil)
	send(t, app, http.MethodGet, "/api/v1", nil)
	shutDown(t)

	var routes []any
	for _, span := range server.Spans(t) {
		routes = append(routes, testutils.Attributes(span.Attributes)["http.route"])
	}
	assert.ElementsMatch(t, []any{"/api/v1/users/:userID", "/api/v1"}, routes)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	send(t, app, http.MethodGet, "/items/1", nil)
	shutDown(t)

	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	server.DecodeStartupEvent(t, &body)
	assert.Equal(t, "fiber", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/"},
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
	app := newApp(cfg)

	resp := send(t, app, http.MethodPost, "/items", strings.NewReader(`{"name": "x", "password": "secret"}`), "Content-Type", "application/json")
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
	app := newApp(cfg)

	resp := send(t, app, http.MethodGet, "/stream", nil)
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
	app := newApp(nil)

	missing := send(t, app, http.MethodGet, "/missing", nil)
	missingInGroup := send(t, app, http.MethodGet, "/api/v1/missing", nil)
	shutDown(t)

	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusNotFound, missingInGroup.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	for _, span := range spans {
		assert.Equal(t, "GET", span.Name)
		attrs := testutils.Attributes(span.Attributes)
		assert.NotContains(t, attrs, "http.route")
		assert.Equal(t, int64(404), attrs["http.response.status_code"])
	}
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
}

func TestSetConsumerReachesSpanAndHistogram(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	send(t, app, http.MethodGet, "/api/v1/users/7", nil, "X-Consumer", "acme")
	shutDown(t)

	span := server.SingleSpan(t)
	assert.Equal(t, "acme", testutils.Attributes(span.Attributes)["apitally.consumer.identifier"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, "acme", testutils.Attributes(points[0].Attributes)["apitally.consumer.identifier"])
}

func TestUnhandledPanicRecordedOnServerSpan(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	resp := send(t, app, http.MethodGet, "/panic", nil)
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	span := server.SingleSpan(t)
	require.Len(t, span.Events, 1)
	attrs := testutils.Attributes(span.Events[0].Attributes)
	assert.Equal(t, "boom", attrs["exception.message"])
	assert.Regexp(t, `^github.com/apitally/apitally-go/fiber-v2_test.newApp.func\d+\n\t\S+/middleware_test.go:\d+\n`, attrs["exception.stacktrace"])
	assert.Equal(t, int64(500), testutils.Attributes(span.Attributes)["http.response.status_code"])
}

func TestValidationErrorReported(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)

	resp := send(t, app, http.MethodPost, "/validate", nil)
	shutDown(t)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
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
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		ctx, span := otel.Tracer("outer-instrumentation").Start(c.UserContext(), "outer", trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		c.SetUserContext(ctx)
		return c.Next()
	})
	apitally.Init(app, nil)
	app.Get("/items/:id", func(c *fiber.Ctx) error { return c.SendString("item") })

	send(t, app, http.MethodGet, "/items/1", nil)
	shutDown(t)

	require.Len(t, userSpans.GetSpans(), 1)
	span := server.SingleSpan(t)
	assert.Equal(t, userSpans.GetSpans()[0].SpanContext.SpanID(), trace.SpanID(span.SpanId))
	assert.Equal(t, "/items/:id", testutils.Attributes(span.Attributes)["http.route"])
}

func TestInitTwiceDoesNotStackMiddleware(t *testing.T) {
	server := setUp(t)
	app := fiber.New()
	apitally.Init(app, nil)
	apitally.Init(app, nil)
	app.Get("/items", func(c *fiber.Ctx) error { return nil })

	send(t, app, http.MethodGet, "/items", nil)
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	app := newApp(cfg)

	item := send(t, app, http.MethodGet, "/items/1", nil)
	missing := send(t, app, http.MethodGet, "/missing", nil)
	failed := send(t, app, http.MethodGet, "/error", nil)
	shutDown(t)

	assert.Equal(t, http.StatusOK, item.StatusCode)
	assert.Equal(t, "item 1", item.Body)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, failed.StatusCode)
	assert.Equal(t, "failed", failed.Body)
	assert.Empty(t, server.Requests())
}

func TestReturnedErrorIsDispatchedToErrorHandlerOnce(t *testing.T) {
	server := setUp(t)
	errorHandlerCalls := 0
	app := newApp(nil, fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		errorHandlerCalls++
		return c.Status(fiber.StatusInternalServerError).SendString("custom: " + err.Error())
	}})

	resp := send(t, app, http.MethodGet, "/error", nil)
	shutDown(t)

	assert.Equal(t, 1, errorHandlerCalls)
	assert.Equal(t, "custom: failed", resp.Body)
	span := server.SingleSpan(t)
	assert.Equal(t, int64(500), testutils.Attributes(span.Attributes)["http.response.status_code"])
	errors := server.Events(t, "apitally.request.server_error")
	require.Len(t, errors, 1)
	assert.Equal(t, "", testutils.Value(errors[0].Body).(map[string]any)["stacktrace"])
}

func TestFailingErrorHandlerFallsBackToStatus500(t *testing.T) {
	server := setUp(t)
	app := newApp(nil, fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		c.Status(fiber.StatusServiceUnavailable)
		return errors.New("rendering the error page failed")
	}})

	resp := send(t, app, http.MethodGet, "/error", nil)
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	span := server.SingleSpan(t)
	assert.Equal(t, int64(500), testutils.Attributes(span.Attributes)["http.response.status_code"])
}

func TestStreamsOfKnownLengthReportSizeWithoutCapture(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureResponseBody = true
	app := fiber.New()
	apitally.Init(app, cfg)
	app.Get("/bytes", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlain)
		return c.SendStream(bytes.NewReader([]byte("12345")))
	})

	send(t, app, http.MethodGet, "/bytes", nil)
	shutDown(t)

	span := server.SingleSpan(t)
	attrs := testutils.Attributes(span.Attributes)
	assert.Equal(t, int64(5), attrs["http.response.body.size"])
	assert.NotContains(t, attrs, "apitally.response.body")
}

func TestStreamWriterRunsInsideServerSpan(t *testing.T) {
	server := setUp(t)
	streamEnd := make(chan time.Time, 1)
	app := fiber.New()
	apitally.Init(app, nil)
	app.Get("/stream", func(c *fiber.Ctx) error {
		// The stream writer runs after the handler returned, when Fiber may have
		// reused c.
		ctx := c.UserContext()
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			_, span := otel.Tracer("test").Start(ctx, "write chunk")
			_, _ = w.WriteString("chunk\n")
			_ = w.Flush()
			span.End()
			streamEnd <- time.Now()
		})
		return nil
	})

	resp := send(t, app, http.MethodGet, "/stream", nil)
	shutDown(t)

	assert.Equal(t, "chunk\n", resp.Body)
	spans := map[string]testutils.Span{}
	for _, span := range server.Spans(t) {
		spans[span.Name] = span
	}
	require.Len(t, spans, 2)
	assert.Equal(t, spans["GET /stream"].SpanId, spans["write chunk"].ParentSpanId)
	assert.GreaterOrEqual(t, int64(spans["GET /stream"].EndTimeUnixNano), (<-streamEnd).UnixNano())
}

func TestUnknownLengthStreamIsClosedAfterResponse(t *testing.T) {
	setUp(t)
	stream := &closableStream{Reader: strings.NewReader("streamed")}
	app := fiber.New()
	apitally.Init(app, nil)
	app.Get("/stream", func(c *fiber.Ctx) error {
		return c.SendStream(stream)
	})

	resp := send(t, app, http.MethodGet, "/stream", nil)
	shutDown(t)

	assert.Equal(t, "streamed", resp.Body)
	assert.Equal(t, 1, stream.closes)
}

// closableStream is a response stream of unknown length that counts its
// Close calls.
type closableStream struct {
	io.Reader
	closes int
}

func (s *closableStream) Close() error {
	s.closes++
	return nil
}

func TestAbortedStreamOmitsSize(t *testing.T) {
	server := setUp(t)
	isDisconnected := make(chan struct{})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	apitally.Init(app, nil)
	app.Get("/stream", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlain)
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			_, _ = w.WriteString("first\n")
			_ = w.Flush()
			<-isDisconnected
			// Writes fail once the client's connection close reaches the server.
			for {
				if _, err := w.WriteString("next\n"); err != nil {
					return
				}
				if w.Flush() != nil {
					return
				}
			}
		})
		return nil
	})
	listener := listen(t, app)

	resp, err := http.Get("http://" + listener + "/stream")
	require.NoError(t, err)
	_, err = resp.Body.Read(make([]byte, 1))
	require.NoError(t, err)
	_ = resp.Body.Close()
	close(isDisconnected)
	require.NoError(t, app.Shutdown())
	shutDown(t)

	span := server.SingleSpan(t)
	attrs := testutils.Attributes(span.Attributes)
	assert.NotContains(t, attrs, "http.response.body.size")
	assert.Equal(t, int64(200), attrs["http.response.status_code"])
}

func TestRequestServedThroughAdaptorIsExported(t *testing.T) {
	server := setUp(t)
	app := newApp(nil)
	url := testutils.Serve(t, adaptor.FiberApp(app))

	resp := testutils.Get(t, url+"/items/42")
	// The adaptor never resets the request context, so the end of a stream of
	// unknown length completes the request.
	streamResp := testutils.Get(t, url+"/stream")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	assert.Equal(t, "chunk 0\nchunk 1\nchunk 2\n", streamResp.Body)
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	sizes := map[string]any{}
	for _, span := range spans {
		sizes[span.Name] = testutils.Attributes(span.Attributes)["http.response.body.size"]
	}
	assert.Equal(t, map[string]any{"GET /items/:id": int64(7), "GET /stream": int64(24)}, sizes)
	assert.Len(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"), 2)
}

func TestListeningAppDeliversTelemetryOnShutdown(t *testing.T) {
	server := setUp(t)
	app := newApp(nil, fiber.Config{DisableStartupMessage: true})

	listen(t, app)
	require.NoError(t, app.Shutdown())

	assert.Len(t, server.Events(t, "apitally.app.startup"), 1)
}

func TestInitAfterRoutesLogsError(t *testing.T) {
	server := setUp(t)
	logs := testutils.RecordSlog(t)
	app := fiber.New()
	app.Get("/early", func(c *fiber.Ctx) error { return nil })
	apitally.Init(app, nil)
	app.Get("/late", func(c *fiber.Ctx) error { return nil })

	send(t, app, http.MethodGet, "/early", nil)
	send(t, app, http.MethodGet, "/late", nil)
	shutDown(t)

	assert.Len(t, logs.Messages(slog.LevelError), 1)
	span := server.SingleSpan(t)
	assert.Equal(t, "GET /late", span.Name)
}

// listen serves app on a local port until the test ends and returns its
// address once the app's OnListen hooks have run.
func listen(t *testing.T, app *fiber.App) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	isListening := make(chan struct{})
	app.Hooks().OnListen(func(fiber.ListenData) error {
		close(isListening)
		return nil
	})
	served := make(chan error, 1)
	go func() { served <- app.Listener(listener) }()
	<-isListening
	t.Cleanup(func() {
		_ = app.Shutdown()
		<-served
	})
	return listener.Addr().String()
}
