package internal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

// startTestApp serves handler behind Apitally's net/http middleware, with
// routes taken from http.ServeMux patterns.
func startTestApp(t *testing.T, handler http.Handler) string {
	return testutils.Serve(t, NetHTTPMiddleware(serveMuxRoute)(handler))
}

func serveMuxRoute(r *http.Request) string {
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

func writeOK(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func TestResponseWriterSupportsStreaming(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	proceed := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		<-proceed
		controller := http.NewResponseController(w)
		assert.NoError(t, controller.Flush())
		// Only the wrapped writer implements SetWriteDeadline, so this goes through Unwrap.
		assert.NoError(t, controller.SetWriteDeadline(time.Now().Add(time.Minute)))
		_, _ = w.Write([]byte("data: 2\n\n"))
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
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, "data: 1\n\n", string(first))
	assert.Equal(t, "data: 2\n\n", string(rest))
	assert.Len(t, server.Spans(t), 1)
}

func TestReadFromUsesWrappedWriterUnlessBodyIsCaptured(t *testing.T) {
	for _, isCaptured := range []bool{false, true} {
		t.Run(strconv.FormatBool(isCaptured), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			cfg := root.NewConfig()
			cfg.CaptureResponseBody = isCaptured
			registerForTest(t, server, cfg)
			contents := strings.Repeat("x", 1000)
			app := NetHTTPMiddleware(serveMuxRoute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.Copy(w, io.LimitReader(strings.NewReader(contents), 1024))
			}))
			recorder := &readerFromRecorder{ResponseRecorder: httptest.NewRecorder()}

			app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/file", nil))
			require.NoError(t, Shutdown(context.Background()))

			assert.Equal(t, contents, recorder.Body.String())
			assert.Equal(t, !isCaptured, recorder.isReadFromCalled)
			spans := server.Spans(t)
			require.Len(t, spans, 1)
			attrs := testutils.Attributes(spans[0].Attributes)
			assert.Equal(t, int64(1000), attrs["http.response.body.size"])
			if isCaptured {
				assert.Equal(t, contents, attrs["apitally.response.body"])
			}
		})
	}
}

func TestRequestBodyIsRecordedOnlyWhenReadToEnd(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.CaptureRequestBody = true
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /full", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	})
	mux.HandleFunc("POST /partial", func(w http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 2))
	})
	appURL := startTestApp(t, mux)

	for _, path := range []string{"/full", "/partial"} {
		// A reader of unknown length makes the client send the body chunked.
		req, _ := http.NewRequest(http.MethodPost, appURL+path, io.MultiReader(strings.NewReader(`{"name":"x"}`)))
		req.Header.Set("Content-Type", "application/json")
		testutils.Do(t, http.DefaultClient.Do, req)
	}
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 2)
	bodies := map[string][]any{}
	for _, span := range spans {
		attrs := testutils.Attributes(span.Attributes)
		bodies[span.Name] = []any{attrs["http.request.body.size"], attrs["apitally.request.body"]}
	}
	assert.Equal(t, map[string][]any{
		"POST /full":    {int64(12), `{"name":"x"}`},
		"POST /partial": {nil, nil},
	}, bodies)
}

func TestDetectedContentTypeIsRecorded(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.CaptureResponseBody = true
	registerForTest(t, server, cfg)
	headerAfterWrite := http.Header{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /write", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
		headerAfterWrite = w.Header().Clone()
	})
	mux.HandleFunc("GET /copy", func(w http.ResponseWriter, r *http.Request) {
		// io.Copy uses the writer's ReadFrom for a source without WriteTo.
		_, _ = io.Copy(w, io.LimitReader(strings.NewReader(`{"id":1}`), 512))
	})
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/write")
	testutils.Get(t, appURL+"/copy")
	require.NoError(t, Shutdown(context.Background()))

	assert.Empty(t, headerAfterWrite.Get("Content-Type"))
	spans := server.Spans(t)
	require.Len(t, spans, 2)
	for _, span := range spans {
		attrs := testutils.Attributes(span.Attributes)
		assert.Equal(t, []any{"text/plain; charset=utf-8"}, attrs["http.response.header.content-type"], span.Name)
		assert.Equal(t, `{"id":1}`, attrs["apitally.response.body"], span.Name)
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
