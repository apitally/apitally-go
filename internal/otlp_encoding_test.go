package internal

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestInvalidUTF8IsReplacedInExportedTelemetry(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		SetConsumer(r.Context(), root.Consumer{Identifier: "acme\xff"})
		trace.SpanFromContext(r.Context()).RecordError(errors.New("item " + r.PathValue("id") + " not found"))
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items/%ff")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "/items/\uFFFD", testutils.Attributes(spans[0].Attributes)["url.path"])
	require.Len(t, spans[0].Events, 1)
	assert.Equal(t, "item \uFFFD not found", testutils.Attributes(spans[0].Events[0].Attributes)["exception.message"])
	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
	require.Len(t, points, 1)
	assert.Equal(t, "acme\uFFFD", testutils.Attributes(points[0].Attributes)["apitally.consumer.identifier"])
}
