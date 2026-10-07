package internal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

// startTestApp serves handler behind Apitally's net/http middleware, with
// routes taken from http.ServeMux patterns.
func startTestApp(t *testing.T, handler http.Handler) string {
	server := httptest.NewServer(NetHTTPMiddleware(serveMuxRoute)(handler))
	t.Cleanup(server.Close)
	return server.URL
}

func serveMuxRoute(r *http.Request) string {
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func writeOK(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func TestResponseWriterKeepsStreamingAndHijacking(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	proceed := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		<-proceed
		_, _ = w.Write([]byte("data: 2\n\n"))
	})
	mux.HandleFunc("GET /raw", func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		defer conn.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 3\r\nConnection: close\r\n\r\nraw")
		_ = buffer.Flush()
	})
	appURL := startTestApp(t, mux)

	resp, err := http.Get(appURL + "/events")
	require.NoError(t, err)
	first := make([]byte, 9)
	_, err = io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	close(proceed)
	rest, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	raw := testutils.Get(t, appURL+"/raw")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, "data: 1\n\n", string(first))
	assert.Equal(t, "data: 2\n\n", string(rest))
	assert.Equal(t, "raw", raw.Body)
	assert.Equal(t, int64(18), testutils.Attributes(findSpan(t, server.Spans(t), "GET /events").Attributes)["http.response.body.size"])
}

func TestReadFromUsesWrappedWriterUnlessBodyIsCaptured(t *testing.T) {
	for _, isCaptured := range []bool{false, true} {
		t.Run(strconv.FormatBool(isCaptured), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			cfg.CaptureResponseBody = isCaptured
			registerForTest(t, server, cfg)
			app := NetHTTPMiddleware(serveMuxRoute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.Copy(w, io.LimitReader(strings.NewReader("file contents"), 1024))
			}))
			recorder := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder()}

			app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/file", nil))
			require.NoError(t, Shutdown(context.Background()))

			assert.Equal(t, "file contents", recorder.Body.String())
			assert.Equal(t, !isCaptured, recorder.isReadFromCalled)
			attrs := testutils.Attributes(server.Spans(t)[0].Attributes)
			assert.Equal(t, int64(13), attrs["http.response.body.size"])
			if isCaptured {
				assert.Equal(t, "file contents", attrs["apitally.response.body"])
			}
		})
	}
}

// readerFromRecorder is a response writer implementing io.ReaderFrom, as
// net/http's own writer does to use sendfile.
type readerFromRecorder struct {
	*httptest.ResponseRecorder
	isReadFromCalled bool
}

func (r *readerFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.isReadFromCalled = true
	return io.Copy(r.ResponseRecorder, src)
}
