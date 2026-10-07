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

// startBodyCaptureApp serves a handler that reads the request body and
// responds with responseBody and contentType.
func startBodyCaptureApp(t *testing.T, server *testutils.OTLPServer, contentType, responseBody string) string {
	cfg := root.NewConfig()
	cfg.CaptureRequestBody, cfg.CaptureResponseBody, cfg.CaptureResponseHeaders = true, true, false
	registerForTest(t, server, cfg)
	return startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(responseBody))
	}))
}

func post(t *testing.T, url, contentType, body string) {
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	testutils.Do(t, http.DefaultClient.Do, req)
}

func TestBodiesWithAllowedContentTypesAreCaptured(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	appURL := startBodyCaptureApp(t, server, "text/plain; charset=utf-8", "pong")

	post(t, appURL+"/items", "Application/JSON", `{"name":"x"}`)
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, `{"name":"x"}`, attrs["apitally.request.body"])
	assert.Equal(t, "pong", attrs["apitally.response.body"])
}

func TestBodiesWithOtherContentTypesAreNotCaptured(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	appURL := startBodyCaptureApp(t, server, "text/html", "<p>pong</p>")

	post(t, appURL+"/items", "application/octet-stream", "ping")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.NotContains(t, attrs, "apitally.request.body")
	assert.NotContains(t, attrs, "apitally.response.body")
	assert.Equal(t, int64(4), attrs["http.request.body.size"])
	assert.Equal(t, int64(11), attrs["http.response.body.size"])
}

func TestBodiesOverLimitAreCapturedAsTooLarge(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	large := `"` + strings.Repeat("x", maxBodySize) + `"`
	appURL := startBodyCaptureApp(t, server, "application/json", large)

	post(t, appURL+"/items", "application/json", large)
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.Equal(t, "[BODY_TOO_LARGE]", attrs["apitally.request.body"])
	assert.Equal(t, "[BODY_TOO_LARGE]", attrs["apitally.response.body"])
	assert.Equal(t, int64(len(large)), attrs["http.response.body.size"])
}

func TestPartiallyReadRequestBodyIsNotCapturedAndHasNoSize(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.CaptureRequestBody = true
	registerForTest(t, server, cfg)
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 2))
	}))

	req, _ := http.NewRequest(http.MethodPost, appURL+"/items", io.MultiReader(strings.NewReader(`{"name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	testutils.Do(t, http.DefaultClient.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	attrs := testutils.Attributes(spans[0].Attributes)
	assert.NotContains(t, attrs, "apitally.request.body")
	assert.NotContains(t, attrs, "http.request.body.size")
}
