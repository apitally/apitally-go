package internal

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

// startBodyCaptureApp serves a handler that responds with the request body
// and content type.
func startBodyCaptureApp(t *testing.T, server *testutils.OTLPServer) string {
	cfg := root.NewConfig()
	cfg.CaptureRequestBody, cfg.CaptureResponseBody, cfg.CaptureResponseHeaders = true, true, false
	registerForTest(t, server, cfg)
	return startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		_, _ = w.Write(body)
	}))
}

func TestBodiesAreCapturedOnlyWithAllowedContentTypes(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	appURL := startBodyCaptureApp(t, server)

	testutils.Send(t, http.MethodPost, appURL+"/json", `{"name":"x"}`, "Content-Type", "Application/JSON")
	testutils.Send(t, http.MethodPost, appURL+"/binary", "ping", "Content-Type", "application/octet-stream")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	bodies := map[any][]any{}
	for _, span := range spans {
		attrs := testutils.Attributes(span.Attributes)
		bodies[attrs["url.path"]] = []any{attrs["apitally.request.body"], attrs["apitally.response.body"], attrs["http.request.body.size"], attrs["http.response.body.size"]}
	}
	assert.Equal(t, map[any][]any{
		"/json":   {`{"name":"x"}`, `{"name":"x"}`, int64(12), int64(12)},
		"/binary": {nil, nil, int64(4), int64(4)},
	}, bodies)
}

func TestBodiesOverLimitAreCapturedAsTooLarge(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	large := `"` + strings.Repeat("x", maxBodySize) + `"`
	appURL := startBodyCaptureApp(t, server)

	testutils.Send(t, http.MethodPost, appURL+"/items", large, "Content-Type", "application/json")
	require.NoError(t, Shutdown(context.Background()))

	span := server.SingleSpan(t)
	attrs := testutils.Attributes(span.Attributes)
	assert.Equal(t, "[BODY_TOO_LARGE]", attrs["apitally.request.body"])
	assert.Equal(t, "[BODY_TOO_LARGE]", attrs["apitally.response.body"])
	assert.Equal(t, int64(len(large)), attrs["http.response.body.size"])
}
