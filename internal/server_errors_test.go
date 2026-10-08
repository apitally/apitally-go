package internal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

// recoverPanics responds with 500 to a panic, as application recovery
// registered before Apitally does, and sends the recovered value to
// recovered.
func recoverPanics(next http.Handler, recovered chan<- any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				recovered <- p
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func TestPanicIsRecordedAsServerError(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	panicValue := fmt.Errorf("loading config: %w", &fs.PathError{Op: "open", Path: "app.yaml", Err: fs.ErrNotExist})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		panic(panicValue)
	})
	recovered := make(chan any, 1)
	appURL := testutils.Serve(t, recoverPanics(NetHTTPMiddleware(serveMuxRoute)(mux), recovered))

	resp := testutils.Get(t, appURL+"/config")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Same(t, panicValue, <-recovered)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	event := testutils.Attributes(spans[0].Events[0].Attributes)
	assert.Equal(t, "exception", spans[0].Events[0].Name)
	assert.Equal(t, "fs.PathError", event["exception.type"])
	assert.Equal(t, "loading config: open app.yaml: file does not exist", event["exception.message"])
	assert.Contains(t, event["exception.stacktrace"], "net/http.HandlerFunc.ServeHTTP\n\t")
	assert.NotContains(t, event["exception.stacktrace"], "runtime.gopanic")
	records := server.Events(t, serverErrorEventName)
	require.Len(t, records, 1)
	assert.Equal(t, map[string]any{
		"method":     "GET",
		"path":       "/config",
		"type":       "fs.PathError",
		"message":    "loading config: open app.yaml: file does not exist",
		"stacktrace": event["exception.stacktrace"],
		"counts":     []any{map[string]any{"count": int64(1)}},
	}, testutils.Value(records[0].Body))
}

func TestOnlyFirstCapturedErrorIsKept(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		CaptureError(r.Context(), context.Canceled)
		CaptureError(r.Context(), errors.New("first"))
		CaptureError(r.Context(), errors.New("second"))
		panic("third")
	})
	appURL := testutils.Serve(t, recoverPanics(NetHTTPMiddleware(serveMuxRoute)(mux), make(chan any, 1)))

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	assert.Equal(t, "first", testutils.Attributes(spans[0].Events[0].Attributes)["exception.message"])
	records := server.Events(t, serverErrorEventName)
	require.Len(t, records, 1)
	assert.Equal(t, "first", testutils.Value(records[0].Body).(map[string]any)["message"])
}

func TestPanicAfterResponseStartedKeepsStatus(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("boom")
	})
	app := recoverPanics(NetHTTPMiddleware(serveMuxRoute)(mux), make(chan any, 1))
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/items", nil))
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, int64(http.StatusAccepted), testutils.Attributes(spans[0].Attributes)["http.response.status_code"])
	assert.Empty(t, server.Events(t, serverErrorEventName))
}

func TestAbortHandlerPanicIsNotCaptured(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})
	app := recoverPanics(NetHTTPMiddleware(serveMuxRoute)(mux), make(chan any, 1))

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Events)
	assert.Empty(t, server.Events(t, serverErrorEventName))
}

func TestServerErrorsAreFilteredByStatus(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, r *http.Request) {
		requestStateFromContext(r.Context()).CaptureReturnedError(errors.New("not found"))
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /unavailable", func(w http.ResponseWriter, r *http.Request) {
		requestStateFromContext(r.Context()).CaptureReturnedError(errors.New("unavailable"))
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("GET /deliberate", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /failed", func(w http.ResponseWriter, r *http.Request) {
		CaptureError(r.Context(), errors.New("failed"))
		w.WriteHeader(http.StatusInternalServerError)
	})
	appURL := startTestApp(t, mux)

	for _, path := range []string{"/missing", "/unavailable", "/deliberate", "/failed", "/failed"} {
		testutils.Get(t, appURL+path)
	}
	require.NoError(t, Shutdown(context.Background()))

	spans := server.Spans(t)
	require.Len(t, spans, 5)
	exceptionMessages := map[string][]any{}
	for _, span := range spans {
		for _, event := range span.Events {
			exceptionMessages[span.Name] = append(exceptionMessages[span.Name], testutils.Attributes(event.Attributes)["exception.message"])
		}
	}
	assert.Equal(t, map[string][]any{"GET /unavailable": {"unavailable"}, "GET /failed": {"failed", "failed"}}, exceptionMessages)
	records := server.Events(t, serverErrorEventName)
	require.Len(t, records, 1)
	body := testutils.Value(records[0].Body).(map[string]any)
	assert.Equal(t, "/failed", body["path"])
	assert.Equal(t, []any{map[string]any{"count": int64(2)}}, body["counts"])
}

type nilPointerError struct{ message string }

func (e *nilPointerError) Error() string { return e.message }

func TestErrorWhoseErrorMethodPanicsIsNotCaptured(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err *nilPointerError
		requestStateFromContext(r.Context()).CaptureReturnedError(err)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	resp := testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Events)
}

func TestServerErrorsAreCountedPerConsumerAndCapped(t *testing.T) {
	var groups errorGroups[string]
	groups.add("first", "acme")
	groups.add("first", "")
	groups.add("first", "acme")
	for i := range maxErrorGroups {
		groups.add(fmt.Sprint(i), "")
	}

	drained := groups.drain()

	assert.Len(t, drained, maxErrorGroups)
	assert.Equal(t, map[string]uint64{"acme": 2, "": 1}, drained["first"])
	assert.Empty(t, groups.drain())
}

func TestExceptionTypeNameRemovesPointerAndSingleWrapping(t *testing.T) {
	pathError := &fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}
	for value, typeName := range map[any]string{
		pathError: "fs.PathError",
		fmt.Errorf("a: %w", fmt.Errorf("b: %w", pathError)): "fs.PathError",
		fmt.Errorf("%w and %w", pathError, fs.ErrClosed):    "fmt.wrapErrors",
		errors.New("plain"): "errors.errorString",
		"boom":              "string",
	} {
		assert.Equal(t, typeName, exceptionTypeName(value))
	}
}
