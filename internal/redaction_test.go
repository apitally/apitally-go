package internal

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestQueryParametersAreRedactedOnAllExportedSpans(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.MaskQueryParams = []string{"^email$"}
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("test").Start(r.Context(), "call upstream")
		span.SetAttributes(attribute.String("url.full", "https://upstream.example/v1?API_KEY=abc&page=2"))
		span.End()
		writeOK(w, r)
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items?access%5Ftoken=abc&Email=a@b.c&page=1&%zzpassword=x")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	assert.Equal(t, "access%5Ftoken=[REDACTED]&Email=[REDACTED]&page=1&%zzpassword=[REDACTED]", testutils.Attributes(findSpan(t, spans, "GET /items").Attributes)["url.query"])
	assert.Equal(t, "https://upstream.example/v1?API_KEY=[REDACTED]&page=2", testutils.Attributes(findSpan(t, spans, "call upstream").Attributes)["url.full"])
}
