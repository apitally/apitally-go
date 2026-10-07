package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/apitally/apitally-go/internal/testutils"
)

func TestOuterServerSpanIsReusedWithApitallyAttributesOnExportCopyOnly(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	synctest.Test(t, func(t *testing.T) {
		registerForTest(t, server, nil)
		userSpans := tracetest.NewInMemoryExporter()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(userSpans)))
		mux := http.NewServeMux()
		mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
			trace.SpanFromContext(r.Context()).End()
			time.Sleep(time.Second)
			writeOK(w, r)
		})
		app := testutils.InstrumentHTTPHandler(NetHTTPMiddleware(serveMuxRoute)(mux))

		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/1", nil))
		require.NoError(t, Shutdown(context.Background()))

		require.Len(t, userSpans.GetSpans(), 1)
		userSpan := userSpans.GetSpans()[0]
		assert.Empty(t, userSpan.Attributes)
		spans := server.Spans(t)
		require.Len(t, spans, 1)
		assert.Equal(t, userSpan.SpanContext.SpanID().String(), trace.SpanID(spans[0].SpanId).String())
		assert.Equal(t, "outer", spans[0].Name)
		attrs := testutils.Attributes(spans[0].Attributes)
		assert.Equal(t, "/items/{id}", attrs["http.route"])
		assert.Equal(t, int64(200), attrs["http.response.status_code"])
		assert.Equal(t, userSpan.EndTime.Add(time.Second).UnixNano(), int64(spans[0].EndTimeUnixNano))
	})
}
