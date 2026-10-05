package chi_test

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func observer(done chan<- *observe.State) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state := &observe.State{}
			r = r.WithContext(observe.Put(r.Context(), state))
			writer := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() { done <- state }()
			defer func() {
				state.Status = writer.Status()
				if state.Status == 0 {
					state.Status = http.StatusOK
				}
			}()
			defer state.Recover()
			next.ServeHTTP(writer, r)
		})
	}
}

func TestRequestContextAndExplicitError(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	for _, explicit := range []bool{false, true} {
		name := "handler-only"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			done := make(chan *observe.State, 1)
			r := chi.NewRouter()
			r.Use(observer(done))
			var requestState *observe.State
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				requestState = observe.Get(r.Context())
				if explicit {
					requestState.Record(first)
					requestState.Record(second)
				}
				http.Error(w, first.Error(), 500)
			})
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			state := <-done
			if requestState != state || state.Status != 500 || response.Code != 500 {
				t.Fatalf("request=%p state=%p status=%d response=%d", requestState, state, state.Status, response.Code)
			}
			if (explicit && state.Error != first) || (!explicit && state.Error != nil) || state.Stack != "" {
				t.Fatalf("unexpected explicit error capture: %+v", state)
			}
		})
	}
}

func TestRecoveryOrderAndServerBaseline(t *testing.T) {
	boom := errors.New("original panic")
	for _, order := range []string{"outside", "inside", "none"} {
		for _, value := range []error{boom, http.ErrAbortHandler} {
			name := order + "/panic"
			if value == http.ErrAbortHandler {
				name = order + "/ErrAbortHandler"
			}
			t.Run(name, func(t *testing.T) {
				baseline := servePanic(t, order, false, value)
				observed := servePanic(t, order, true, value)
				aborted := order == "none" || value == http.ErrAbortHandler
				if aborted {
					if !errors.Is(baseline.err, io.EOF) || !errors.Is(observed.err, io.EOF) || baseline.status != 0 || observed.status != 0 {
						t.Fatalf("expected connection abort, baseline=%+v observed=%+v", baseline, observed)
					}
				} else if baseline.err != nil || observed.err != nil || baseline.status != 500 || observed.status != baseline.status || observed.body != baseline.body {
					t.Fatalf("observer changed recovery response, baseline=%+v observed=%+v", baseline, observed)
				}
				if baseline.hasContext || !observed.hasContext || observed.state == nil {
					t.Fatalf("request context state unavailable: baseline=%t observed=%t", baseline.hasContext, observed.hasContext)
				}
				state := observed.state
				captured := value != http.ErrAbortHandler && order != "inside"
				wantStatus := 200
				if order == "inside" && value != http.ErrAbortHandler {
					wantStatus = 500
				}
				if state.Status != wantStatus {
					t.Fatalf("observed status=%d, want %d (not necessarily the client status)", state.Status, wantStatus)
				}
				if captured {
					if state.Panic != value || state.Error != value || !strings.Contains(state.Stack, ".chiPanic\n") || len(state.RawFunctions) == 0 {
						t.Fatalf("missing panic capture: %+v", state)
					}
					if strings.Contains(state.Stack, "goroutine ") || strings.Contains(state.Stack, "+0x") {
						t.Fatalf("unexpected stack format: %s", state.Stack)
					}
				} else if state.Panic != nil || state.Error != nil || state.Stack != "" || len(state.RawFunctions) != 0 {
					t.Fatalf("unexpected capture of recovered or abort panic: %+v", state)
				}
				for _, result := range []panicResult{baseline, observed} {
					if order != "inside" || value == http.ErrAbortHandler {
						if result.repanicked != value {
							t.Fatalf("panic identity lost: %v", result.repanicked)
						}
					} else if result.repanicked != nil {
						t.Fatal("inner Recoverer did not consume panic")
					}
					if order != "none" && value != http.ErrAbortHandler {
						if result.recovered != value {
							t.Fatalf("Recoverer received different panic: %v", result.recovered)
						}
					} else if result.recovered != nil {
						t.Fatal("abort panic unexpectedly logged by Recoverer")
					}
					wantServerLog := order == "none" && value != http.ErrAbortHandler
					if wantServerLog {
						if !strings.Contains(result.serverLog, "http: panic serving") || !strings.Contains(result.serverLog, value.Error()) {
							t.Fatalf("missing net/http panic log: %q", result.serverLog)
						}
					} else if result.serverLog != "" {
						t.Fatalf("unexpected net/http log: %q", result.serverLog)
					}
				}
			})
		}
	}
}

type panicResult struct {
	status     int
	body       string
	err        error
	state      *observe.State
	hasContext bool
	repanicked any
	recovered  any
	serverLog  string
}

func servePanic(t *testing.T, order string, instrumented bool, value any) panicResult {
	t.Helper()
	done := make(chan *observe.State, 1)
	contextSeen := make(chan bool, 1)
	repanicked := make(chan any, 1)
	recovered := make(chan any, 1)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, middleware.WithLogEntry(r, panicLog{recovered}))
		})
	})
	if order == "outside" {
		r.Use(middleware.Recoverer)
	}
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					repanicked <- v
					panic(v)
				}
			}()
			next.ServeHTTP(w, r)
		})
	})
	if instrumented {
		r.Use(observer(done))
	}
	if order == "inside" {
		r.Use(middleware.Recoverer)
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		contextSeen <- observe.Get(r.Context()) != nil
		chiPanic(value)
	})
	var serverLog synchronizedBuffer
	server := httptest.NewUnstartedServer(r)
	server.Config.ErrorLog = log.New(&serverLog, "", 0)
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Get(server.URL)
	result := panicResult{err: err}
	if response != nil {
		result.status = response.StatusCode
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read response: %v", readErr)
		}
		result.body = string(body)
	}
	// Close waits for handlers and net/http's deferred recovery/logging to finish.
	server.Close()
	result.serverLog = serverLog.String()
	select {
	case result.hasContext = <-contextSeen:
	default:
		t.Fatal("panic handler was not called")
	}
	if instrumented {
		select {
		case result.state = <-done:
		default:
			t.Fatal("observer did not complete")
		}
	}
	select {
	case result.repanicked = <-repanicked:
	default:
	}
	select {
	case result.recovered = <-recovered:
	default:
	}
	return result
}

func chiPanic(value any) { panic(value) }

type panicLog struct{ values chan<- any }

func (entry panicLog) Write(int, int, http.Header, time.Duration, interface{}) {}
func (entry panicLog) Panic(value interface{}, _ []byte)                       { entry.values <- value }

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
