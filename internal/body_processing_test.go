package internal

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCompressedResponseBodiesAreDecompressedBeforeRedaction(t *testing.T) {
	body := []byte(`{"password":"secret","id":1}`)
	var gzipped, deflated bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipped)
	_, _ = gzipWriter.Write(body)
	_ = gzipWriter.Close()
	zlibWriter := zlib.NewWriter(&deflated)
	_, _ = zlibWriter.Write(body)
	_ = zlibWriter.Close()
	for _, tc := range []struct {
		name     string
		encoding string
		body     []byte
		captured any
	}{
		{"gzip is decompressed", "gzip", gzipped.Bytes(), `{"password":"[REDACTED]","id":1}`},
		{"deflate is decompressed", "deflate", deflated.Bytes(), `{"password":"[REDACTED]","id":1}`},
		{"truncated gzip is redacted", "gzip", gzipped.Bytes()[:gzipped.Len()-4], "[REDACTED]"},
		{"unsupported encoding is not captured", "br", []byte("brotli"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			cfg.CaptureResponseBody = true
			registerForTest(t, server, cfg)
			appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Encoding", tc.encoding)
				_, _ = w.Write(tc.body)
			}))

			req, _ := http.NewRequest(http.MethodGet, appURL+"/items", nil)
			req.Header.Set("Accept-Encoding", tc.encoding)
			testutils.Do(t, http.DefaultClient.Do, req)
			require.NoError(t, Shutdown(context.Background()))

			spans := server.Spans(t)
			require.Len(t, spans, 1)
			assert.Equal(t, tc.captured, testutils.Attributes(spans[0].Attributes)["apitally.response.body"])
		})
	}
}

func TestMaskCallbacksReplaceBodiesAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mask     func(sdktrace.ReadOnlySpan, []byte) []byte
		captured string
	}{
		{"replacement", func(_ sdktrace.ReadOnlySpan, body []byte) []byte {
			return bytes.ReplaceAll(body, []byte("alice"), []byte("***"))
		}, `{"name":"***","token":"[REDACTED]"}`},
		{"nil", func(sdktrace.ReadOnlySpan, []byte) []byte { return nil }, "[REDACTED]"},
		{"panic", func(sdktrace.ReadOnlySpan, []byte) []byte { panic("bug") }, "[REDACTED]"},
		{"over limit", func(sdktrace.ReadOnlySpan, []byte) []byte { return make([]byte, maxBodySize+1) }, "[BODY_TOO_LARGE]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			var maskedSpanAttributes []attribute.KeyValue
			cfg := root.NewConfig()
			cfg.CaptureRequestBody = true
			cfg.MaskRequestBody = func(span sdktrace.ReadOnlySpan, body []byte) []byte {
				maskedSpanAttributes = span.Attributes()
				return tc.mask(span, body)
			}
			registerForTest(t, server, cfg)
			testutils.RecordSlog(t)
			appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = r.Body.Read(make([]byte, 1024))
			}))

			post(t, appURL+"/items", "application/json", `{ "name": "alice", "token": "abc" }`)
			require.NoError(t, Shutdown(context.Background()))

			spans := server.Spans(t)
			require.Len(t, spans, 1)
			assert.Equal(t, tc.captured, testutils.Attributes(spans[0].Attributes)["apitally.request.body"])
			assert.Contains(t, maskedSpanAttributes, attribute.StringSlice("http.response.header.content-type", []string{"text/plain"}))
			assert.NotContains(t, attributeKeys(maskedSpanAttributes), "apitally.request.body")
		})
	}
}

func TestJSONBodyFieldsAreRedactedInNestedValuesKeepingKeyOrder(t *testing.T) {
	red := newRedaction(&settings{maskBodyFields: compileDefaultPatterns("^email$")})

	redacted, ok := red.redactJSON([]byte(`{"z": 1, "items": [{"Card_Number": "4111", "email": "a@b.c", "auth": {"pwd": "x"}}], "note": "<b>&", "n": 1.50}`))

	require.True(t, ok)
	assert.Equal(t, `{"z":1,"items":[{"Card_Number":"[REDACTED]","email":"[REDACTED]","auth":{"pwd":"[REDACTED]"}}],"note":"<b>&","n":1.50}`, redacted)
	_, ok = red.redactJSON([]byte("{\"a\":1}\n{\"b\":2}"))
	assert.False(t, ok)
}

func attributeKeys(attrs []attribute.KeyValue) []string {
	keys := make([]string, len(attrs))
	for i, kv := range attrs {
		keys[i] = string(kv.Key)
	}
	return keys
}
