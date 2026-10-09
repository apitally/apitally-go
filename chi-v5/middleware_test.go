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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/validator/v10"
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
	testutils.SetSlogDefault(t, apitally.NewSlogHandler(slog.NewTextHandler(io.Discard, nil)))
	return server
}

var validate = validator.New()

type item struct {
	Name  string `validate:"required"`
	Price int    `validate:"gte=10"`
}

func newRouter(cfg *apitally.Config, middlewares ...func(http.Handler) http.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	apitally.Init(r, cfg)
	r.Use(middlewares...)
	r.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		slog.InfoContext(r.Context(), "fetching item", "id", chi.URLParam(r, "id"))
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
	r.Get("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic(errors.New("boom"))
	})
	r.Post("/validate", func(w http.ResponseWriter, r *http.Request) {
		if err := validate.Struct(item{Name: "", Price: 5}); err != nil {
			apitally.CaptureValidationError(r.Context(), err)
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if id := r.Header.Get("X-Consumer"); id != "" {
					apitally.SetConsumer(r.Context(), apitally.Consumer{Identifier: id, Name: "Acme Corp"})
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("api"))
		})
		// Modular apps mount routers at the root of a mounted router.
		r.Route("/", func(r chi.Router) {
			r.Get("/users/{userID}", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("user"))
			})
		})
	})
	return r
}

func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(t.Context()))
}

func TestRequestExportsSingleServerSpanWithStableSemconv(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil))

	resp := testutils.Get(t, appURL+"/items/42?page=2")
	shutDown(t)

	assert.Equal(t, "item 42", resp.Body)
	span := server.SingleSpan(t)
	parsedURL, _ := url.Parse(appURL)
	port, _ := strconv.Atoi(parsedURL.Port())
	assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, span.Kind)
	assert.Equal(t, "GET /items/{id}", span.Name)
	assert.Equal(t, "github.com/apitally/apitally-go/chi-v5", span.Scope)
	assert.Equal(t, map[string]any{
		"http.request.method":               "GET",
		"url.scheme":                        "http",
		"server.address":                    "127.0.0.1",
		"server.port":                       int64(port),
		"url.path":                          "/items/42",
		"url.query":                         "page=2",
		"user_agent.original":               "Go-http-client/1.1",
		"http.route":                        "/items/{id}",
		"http.response.status_code":         int64(200),
		"client.address":                    "127.0.0.1",
		"http.request.body.size":            int64(0),
		"http.response.body.size":           int64(7),
		"http.response.header.content-type": []any{"text/plain; charset=utf-8"},
	}, testutils.Attributes(span.Attributes))
}

func TestClientAddressUsesFrameworkResolvedClientIP(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil, middleware.RealIP))

	testutils.Get(t, appURL+"/items/1", "X-Forwarded-For", "203.0.113.7")
	shutDown(t)

	span := server.SingleSpan(t)
	assert.Equal(t, "203.0.113.7", testutils.Attributes(span.Attributes)["client.address"])
}

func TestHistogramAttributesAndLogCorrelation(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil))

	testutils.Get(t, appURL+"/items/42")
	shutDown(t)

	span := server.SingleSpan(t)
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, uint64(1), points[0].Count)
	assert.Equal(t, "/items/{id}", testutils.Attributes(points[0].Attributes)["http.route"])
	logs := server.ApplicationLogRecords(t)
	require.Len(t, logs, 1)
	assert.Equal(t, "fetching item", logs[0].Body.GetStringValue())
	assert.Equal(t, span.TraceId, logs[0].TraceId)
	assert.Equal(t, trace.SpanID(span.SpanId).String(), testutils.Attributes(logs[0].Attributes)["apitally.request.server_span_id"])
}

func TestRouteIncludesGroupPrefix(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil, middleware.GetHead))

	testutils.Get(t, appURL+"/api/v1/users/7")
	testutils.Get(t, appURL+"/api/v1")
	// GetHead serves HEAD requests with GET routes.
	testutils.Send(t, http.MethodHead, appURL+"/api/v1/users/7", "")
	shutDown(t)

	var routes []any
	for _, span := range server.Spans(t) {
		routes = append(routes, testutils.Attributes(span.Attributes)["http.route"])
	}
	assert.ElementsMatch(t, []any{"/api/v1/users/{userID}", "/api/v1", "/api/v1/users/{userID}"}, routes)
}

func TestStartupEventPathsMatchRoutes(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	var body struct {
		Framework string              `json:"framework"`
		Paths     []map[string]string `json:"paths"`
	}
	server.DecodeStartupEvent(t, &body)
	assert.Equal(t, "chi", body.Framework)
	assert.ElementsMatch(t, []map[string]string{
		{"method": "GET", "path": "/items/{id}"},
		{"method": "POST", "path": "/items"},
		{"method": "GET", "path": "/stream"},
		{"method": "GET", "path": "/panic"},
		{"method": "POST", "path": "/validate"},
		{"method": "GET", "path": "/api/v1"},
		{"method": "GET", "path": "/api/v1/users/{userID}"},
	}, body.Paths)
}

func TestRequestAndResponseBodiesCapturedAndRedacted(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.CaptureRequestBody = true
	cfg.CaptureResponseBody = true
	appURL := testutils.Serve(t, newRouter(cfg))

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
	appURL := testutils.Serve(t, newRouter(cfg))

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
	appURL := testutils.Serve(t, newRouter(nil))

	resp := testutils.Get(t, appURL+"/api/v1/missing")
	shutDown(t)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	span := server.SingleSpan(t)
	assert.Equal(t, "GET", span.Name)
	attrs := testutils.Attributes(span.Attributes)
	assert.NotContains(t, attrs, "http.route")
	assert.Equal(t, int64(404), attrs["http.response.status_code"])
	assert.Empty(t, testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration"))
}

func TestSetConsumerReachesSpanAndHistogram(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil))

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
	appURL := testutils.Serve(t, newRouter(nil))

	resp := testutils.Get(t, appURL+"/panic")
	shutDown(t)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	span := server.SingleSpan(t)
	require.Len(t, span.Events, 1)
	attrs := testutils.Attributes(span.Events[0].Attributes)
	assert.Equal(t, "boom", attrs["exception.message"])
	assert.Regexp(t, `^github.com/apitally/apitally-go/chi-v5_test.newRouter.func\d+\n\t\S+/middleware_test.go:\d+\n`, attrs["exception.stacktrace"])
	assert.Equal(t, int64(500), testutils.Attributes(span.Attributes)["http.response.status_code"])
}

func TestValidationErrorReported(t *testing.T) {
	server := setUp(t)
	appURL := testutils.Serve(t, newRouter(nil))

	testutils.Send(t, http.MethodPost, appURL+"/validate", "")
	shutDown(t)

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
	appURL := testutils.Serve(t, testutils.InstrumentHTTPHandler(newRouter(nil)))

	testutils.Get(t, appURL+"/items/1")
	shutDown(t)

	require.Len(t, userSpans.GetSpans(), 1)
	span := server.SingleSpan(t)
	assert.Equal(t, userSpans.GetSpans()[0].SpanContext.SpanID(), trace.SpanID(span.SpanId))
	assert.Equal(t, "/items/{id}", testutils.Attributes(span.Attributes)["http.route"])
}

func TestInitTwiceDoesNotStackMiddleware(t *testing.T) {
	server := setUp(t)
	r := chi.NewRouter()
	apitally.Init(r, nil)
	apitally.Init(r, nil)
	r.Get("/items", func(w http.ResponseWriter, r *http.Request) {})
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	assert.Len(t, server.Spans(t), 1)
}

func TestDisabledSDKLeavesResponsesUnchanged(t *testing.T) {
	server := setUp(t)
	cfg := apitally.NewConfig()
	cfg.Disabled = true
	appURL := testutils.Serve(t, newRouter(cfg))

	item := testutils.Get(t, appURL+"/items/1")
	missing := testutils.Get(t, appURL+"/missing")
	shutDown(t)

	assert.Equal(t, http.StatusOK, item.StatusCode)
	assert.Equal(t, "item 1", item.Body)
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Empty(t, server.Requests())
}
