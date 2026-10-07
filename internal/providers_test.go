package internal

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/apitally/apitally-go/internal/testutils"
)

const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

func TestAttachesToGlobalSDKTracerProviderWithApitallyResourceOnExportCopies(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	userSpans := tracetest.NewInMemoryExporter()
	userResource := resource.NewSchemaless(attribute.String("service.name", "shop"), attribute.String("deployment.environment.name", "user-env"))
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(userSpans), sdktrace.WithResource(userResource)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("test").Start(r.Context(), "load items")
		span.End()
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Len(t, userSpans.GetSpans(), 2)
	assert.Equal(t, userResource, userSpans.GetSpans()[0].Resource)
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	for _, span := range spans {
		attrs := testutils.Attributes(span.Resource.Attributes)
		assert.Equal(t, "shop", attrs["service.name"])
		assert.Equal(t, "dev", attrs["deployment.environment.name"])
		assert.Equal(t, instanceID, attrs["service.instance.id"])
	}
}

func TestOwnProviderAndPropagatorAreRegisteredWhenUnset(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))

	req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
	req.Header.Set("traceparent", traceparent)
	testutils.Do(t, http.DefaultClient.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	assert.IsType(t, &sdktrace.TracerProvider{}, otel.GetTracerProvider())
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", trace.TraceID(spans[0].TraceId).String())
	assert.Equal(t, "b7ad6b7169203331", trace.SpanID(spans[0].ParentSpanId).String())
}

func TestApplicationPropagatorIsKept(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	empty := propagation.NewCompositeTextMapPropagator()
	otel.SetTextMapPropagator(empty)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))

	req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
	req.Header.Set("traceparent", traceparent)
	testutils.Do(t, http.DefaultClient.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, empty, otel.GetTextMapPropagator())
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].ParentSpanId)
}

func TestForeignTracerProviderGetsPrivateProviderWithoutDescendants(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	logs := testutils.RecordSlog(t)
	registerForTest(t, server, nil)
	otel.SetTracerProvider(noop.NewTracerProvider())
	isRequestSpanInContext := true
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		isRequestSpanInContext = trace.SpanContextFromContext(r.Context()).IsValid()
		_, span := otel.Tracer("test").Start(r.Context(), "load items")
		span.End()
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Len(t, server.Spans(t), 2)
	assert.False(t, isRequestSpanInContext)
	assert.Len(t, logs.Messages(slog.LevelWarn), 1)
}
