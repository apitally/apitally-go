package fiberv2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
)

func TestReturnedErrorsMatchBaseline(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"400", fiber.NewError(400, "bad request"), 400},
		{"422", fiber.NewError(422, "invalid input"), 422},
		{"500", fiber.NewError(500, "server error"), 500},
		{"plain", errors.New("plain error"), 500},
		{"wrapped", fmt.Errorf("wrapped: %w", fiber.NewError(422, "invalid input")), 422},
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("custom=%t", custom), func(t *testing.T) {
					scenario := scenario{handler: func(*fiber.Ctx) error { return test.err }}
					wantStatus, wantBody := test.status, test.err.Error()
					if custom {
						scenario.errorHandler = func(c *fiber.Ctx, err error) error {
							return c.Status(409).SendString("custom: " + err.Error())
						}
						wantStatus, wantBody = 409, "custom: "+test.err.Error()
					}
					baseline := exercise(t, scenario)
					scenario.observed = true
					observed := exercise(t, scenario)
					assertResponse(t, baseline, observed, wantStatus, wantBody)
					if baseline.calls != 1 || observed.calls != 1 {
						t.Fatalf("error handler calls: baseline=%d observed=%d", baseline.calls, observed.calls)
					}
					if observed.state.Status != observed.status || observed.outerError != nil {
						t.Fatalf("observer status=%d final=%d outer error=%v", observed.state.Status, observed.status, observed.outerError)
					}
					wantError := test.err
					if errors.Is(test.err, context.Canceled) {
						wantError = nil
					}
					if observed.state.Error != wantError || observed.state.Stack != "" {
						t.Fatalf("captured error=%v stack=%q", observed.state.Error, observed.state.Stack)
					}
					if baseline.outerError != test.err {
						t.Fatalf("baseline outer error=%v", baseline.outerError)
					}
				})
			}
		})
	}
}

func TestReturnOriginalDuplicatesDispatch(t *testing.T) {
	original := errors.New("original")
	for _, returnOriginal := range []bool{false, true} {
		t.Run(fmt.Sprintf("returnOriginal=%t", returnOriginal), func(t *testing.T) {
			var calls atomic.Int32
			got := exercise(t, scenario{
				observed: true, returnOriginal: returnOriginal,
				handler: func(*fiber.Ctx) error { return original },
				errorHandler: func(c *fiber.Ctx, err error) error {
					call := calls.Add(1)
					return c.Status(420 + int(call)).SendString(fmt.Sprintf("call %d: %v", call, err))
				},
			})
			wantCalls, wantOuter := int32(1), error(nil)
			if returnOriginal {
				wantCalls, wantOuter = 2, original
			}
			if got.calls != wantCalls || got.outerError != wantOuter || got.state.Error != original {
				t.Fatalf("calls=%d outer=%v recorded=%v", got.calls, got.outerError, got.state.Error)
			}
			if got.state.Status != 421 || got.status != 420+int(wantCalls) || got.body != fmt.Sprintf("call %d: original", wantCalls) {
				t.Fatalf("observed status=%d final=%d body=%q", got.state.Status, got.status, got.body)
			}
		})
	}
}

func TestFailedErrorHandlerMatchesRouterFallback(t *testing.T) {
	scenario := scenario{
		handler:      func(*fiber.Ctx) error { return errors.New("route failed") },
		errorHandler: func(*fiber.Ctx, error) error { return errors.New("error handler failed") },
	}
	baseline := exercise(t, scenario)
	scenario.observed = true
	got := exercise(t, scenario)
	assertResponse(t, baseline, got, 500, "Internal Server Error")
	if got.calls != 1 || baseline.calls != 1 || got.state.Status != 500 || got.outerError != nil {
		t.Fatalf("calls baseline=%d observed=%d status=%d outer=%v", baseline.calls, got.calls, got.state.Status, got.outerError)
	}
}

func TestUserContextStateAndFirstError(t *testing.T) {
	first, returned := errors.New("explicit first"), errors.New("returned second")
	contextChecks := make(chan bool, 1)
	got := exercise(t, scenario{observed: true, handler: func(c *fiber.Ctx) error {
		state := observe.Get(c.UserContext())
		contextChecks <- state != nil && c.UserContext().Value(contextKey{}) == "preserved"
		if state != nil {
			state.Record(context.Canceled)
			state.Record(first)
		}
		return returned
	}})
	if !<-contextChecks || got.state.Error != first || got.state.Status != 500 {
		t.Fatalf("context state=%+v", got.state)
	}
	if observe.Get(context.Background()) != nil {
		t.Fatal("state leaked outside the request context")
	}
}

func TestPanicCaptureAndOriginalRepanic(t *testing.T) {
	for _, value := range []any{errors.New("panic error"), &struct{ Message string }{"panic pointer"}, "panic string", http.ErrAbortHandler, fmt.Errorf("wrapped: %w", context.Canceled)} {
		t.Run(fmt.Sprintf("%T/%v", value, value), func(t *testing.T) {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("custom=%t", custom), func(t *testing.T) {
					scenario := scenario{recovery: true, handler: func(c *fiber.Ctx) error {
						c.Status(418)
						panicFromHandler(value)
						return nil
					}}
					wantStatus, wantBody := 500, fmt.Sprint(value)
					if custom {
						scenario.errorHandler = func(c *fiber.Ctx, err error) error {
							return c.Status(503).SendString("recovered: " + err.Error())
						}
						wantStatus, wantBody = 503, "recovered: "+fmt.Sprint(value)
					}
					baseline := exercise(t, scenario)
					scenario.observed = true
					got := exercise(t, scenario)
					assertResponse(t, baseline, got, wantStatus, wantBody)
					if baseline.panicValue != value || got.panicValue != value || baseline.calls != 1 || got.calls != 1 {
						t.Fatalf("panic identity baseline=%v observed=%v calls=%d/%d", baseline.panicValue, got.panicValue, baseline.calls, got.calls)
					}
					// Recovery is outside the observer; its response is written after the observer unwinds.
					if got.state.Status != 418 || got.state.Status == got.status {
						t.Fatalf("unwind status=%d final=%d", got.state.Status, got.status)
					}
					panicError, _ := value.(error)
					if value == http.ErrAbortHandler || errors.Is(panicError, context.Canceled) {
						if got.state.Panic != nil || got.state.Error != nil || got.state.Stack != "" {
							t.Fatalf("excluded panic captured: %+v", got.state)
						}
						return
					}
					if got.state.Panic != value || got.state.Error == nil || got.state.Error.Error() != fmt.Sprint(value) {
						t.Fatalf("panic state=%+v", got.state)
					}
					if err, ok := value.(error); ok && got.state.Error != err {
						t.Fatal("panic error identity changed")
					}
					if !strings.Contains(got.state.Stack, "fiberv2.panicFromHandler\n\t") || strings.Contains(got.state.Stack, "+0x") || strings.Contains(got.state.Stack, "goroutine ") {
						t.Fatalf("panic stack=%q", got.state.Stack)
					}
				})
			}
		})
	}
}

func TestOuterLoggerLosesHandledError(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(fmt.Sprintf("observed=%t", observed), func(t *testing.T) {
			var output bytes.Buffer
			got := exercise(t, scenario{
				observed: observed,
				outer:    logger.New(logger.Config{Format: "${status}|${error}", Output: &output}),
				handler:  func(*fiber.Ctx) error { return errors.New("logger error") },
			})
			want := "500|logger error"
			if observed {
				want = "500|-"
			}
			if output.String() != want || got.calls != 1 || got.status != 500 {
				t.Fatalf("logger=%q calls=%d status=%d", output.String(), got.calls, got.status)
			}
		})
	}
}

// Fiber v2.52.15 middleware/logger/logger.go:119-128,174 handles the error then returns nil.
// router.go:171-174 uses SendStatus(500) when the application's error handler fails.
func middleware(app *fiber.App, returnOriginal bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		state := new(observe.State)
		c.SetUserContext(observe.Put(c.UserContext(), state))
		defer func() { state.Status = c.Response().StatusCode() }()
		defer state.Recover()
		err := c.Next()
		state.Record(err)
		if err != nil {
			if handlerErr := app.ErrorHandler(c, err); handlerErr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}
		if returnOriginal {
			return err
		}
		return nil
	}
}

type scenario struct {
	observed       bool
	returnOriginal bool
	recovery       bool
	handler        fiber.Handler
	errorHandler   fiber.ErrorHandler
	outer          fiber.Handler
}

type result struct {
	status     int
	body       string
	calls      int32
	state      observe.State
	outerError error
	panicValue any
}

type contextKey struct{}

func exercise(t *testing.T, scenario scenario) result {
	t.Helper()
	var calls atomic.Int32
	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		calls.Add(1)
		if scenario.errorHandler != nil {
			return scenario.errorHandler(c, err)
		}
		return fiber.DefaultErrorHandler(c, err)
	}})
	snapshots := make(chan result, 1)
	panics := make(chan any, 1)
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(context.WithValue(c.UserContext(), contextKey{}, "preserved"))
		err := c.Next()
		snapshot := result{outerError: err}
		if state := observe.Get(c.UserContext()); state != nil {
			snapshot.state = *state
		}
		snapshots <- snapshot
		return err
	})
	if scenario.outer != nil {
		app.Use(scenario.outer)
	}
	if scenario.recovery {
		app.Use(fiberrecover.New(fiberrecover.Config{
			EnableStackTrace:  true,
			StackTraceHandler: func(_ *fiber.Ctx, value any) { panics <- value },
		}))
	}
	if scenario.observed {
		app.Use(middleware(app, scenario.returnOriginal))
	}
	app.Get("/test", scenario.handler)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/test", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	got := <-snapshots
	got.status, got.body, got.calls = response.StatusCode, string(body), calls.Load()
	if scenario.recovery {
		got.panicValue = <-panics
	}
	return got
}

func assertResponse(t *testing.T, baseline, got result, status int, body string) {
	t.Helper()
	if baseline.status != status || baseline.body != body || got.status != baseline.status || got.body != baseline.body {
		t.Fatalf("baseline=(%d,%q) observed=(%d,%q) want=(%d,%q)", baseline.status, baseline.body, got.status, got.body, status, body)
	}
}

//go:noinline
func panicFromHandler(value any) {
	panic(value)
}
