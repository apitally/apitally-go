package internal

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCompressedBodiesAreDecompressed(t *testing.T) {
	body := []byte(`{"password":"secret","id":1}`)
	var gzipped, deflated, gzippedLarge bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipped)
	_, _ = gzipWriter.Write(body)
	_ = gzipWriter.Close()
	gzipWriter = gzip.NewWriter(&gzippedLarge)
	_, _ = gzipWriter.Write(append(bytes.Repeat([]byte(" "), maxBodySize), body...))
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
		{"gzip decompressing beyond the limit is too large", "gzip", gzippedLarge.Bytes(), "[BODY_TOO_LARGE]"},
		{"unsupported encoding is not captured", "br", []byte("brotli"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			// The encodings are read even when headers are not captured.
			cfg.CaptureRequestBody, cfg.CaptureResponseBody, cfg.CaptureResponseHeaders = true, true, false
			registerForTest(t, server, cfg)
			appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Request decompression middleware removes the Content-Encoding header.
				r.Header.Del("Content-Encoding")
				_, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Encoding", tc.encoding)
				_, _ = w.Write(tc.body)
			}))

			testutils.Send(t, http.MethodPost, appURL+"/items", string(tc.body), "Content-Type", "application/json", "Content-Encoding", tc.encoding, "Accept-Encoding", tc.encoding)
			require.NoError(t, Shutdown(context.Background()))

			span := server.SingleSpan(t)
			attrs := testutils.Attributes(span.Attributes)
			assert.Equal(t, tc.captured, attrs["apitally.request.body"])
			assert.Equal(t, tc.captured, attrs["apitally.response.body"])
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
			cfg.CaptureRequestBody, cfg.CaptureResponseBody = true, true
			cfg.MaskRequestBody = func(span sdktrace.ReadOnlySpan, body []byte) []byte {
				maskedSpanAttributes = span.Attributes()
				return tc.mask(span, body)
			}
			cfg.MaskResponseBody = tc.mask
			registerForTest(t, server, cfg)
			testutils.RecordSlog(t)
			appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))

			testutils.Send(t, http.MethodPost, appURL+"/items", `{ "name": "alice", "token": "abc" }`, "Content-Type", "application/json")
			require.NoError(t, Shutdown(context.Background()))

			span := server.SingleSpan(t)
			attrs := testutils.Attributes(span.Attributes)
			assert.Equal(t, tc.captured, attrs["apitally.request.body"])
			assert.Equal(t, tc.captured, attrs["apitally.response.body"])
			assert.Contains(t, maskedSpanAttributes, attribute.StringSlice("http.response.header.content-type", []string{"application/json"}))
			assert.False(t, slices.ContainsFunc(maskedSpanAttributes, func(kv attribute.KeyValue) bool { return kv.Key == "apitally.request.body" }))
		})
	}
}

func TestNestedJSONBodyFieldsAreRedacted(t *testing.T) {
	red := newRedaction(&settings{maskBodyFields: compileDefaultPatterns("^email$")})

	// Some clients, such as Windows PowerShell 5.1, start JSON with a UTF-8 byte order mark.
	redacted, ok := red.redactJSON([]byte("\xef\xbb\xbf" + `{"z": 1, "items": [{"Card_Number": "4111", "email": "a@b.c", "auth": {"pwd": "x"}}], "note": "<b>&", "n": 1.50}`))

	require.True(t, ok)
	assert.Equal(t, `{"z":1,"items":[{"Card_Number":"[REDACTED]","email":"[REDACTED]","auth":{"pwd":"[REDACTED]"}}],"note":"<b>&","n":1.50}`, redacted)
	_, ok = red.redactJSON([]byte("{\"a\":1}\n{\"b\":2}"))
	assert.False(t, ok)
}
