package echov5

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func TestReturnedErrorsAndRequestLoggerPrecedent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		missing bool
	}{
		{"400", echo.NewHTTPError(400, "bad input"), 400, false},
		{"404", echo.NewHTTPError(404, "missing item"), 404, false},
		{"unmatched404", echo.ErrNotFound, 404, true},
		{"422", echo.NewHTTPError(422, "invalid input"), 422, false},
		{"plain500", errors.New("database unavailable"), 500, false},
	} {
		for _, handler := range []string{"default", "guarded-custom", "unguarded-custom"} {
			t.Run(tc.name+"/"+handler, func(t *testing.T) {
				run := func(mode string) (*httptest.ResponseRecorder, *observe.State, error, int, int) {
					e := echo.New()
					calls, writes := 0, 0
					defaultHandler := e.HTTPErrorHandler
					e.HTTPErrorHandler = func(c *echo.Context, err error) {
						calls++
						response, _ := echo.UnwrapResponse(c.Response())
						if handler == "default" {
							if !response.Committed {
								writes++
							}
							defaultHandler(c, err)
							return
						}
						if handler == "guarded-custom" && response.Committed {
							return
						}
						writes++
						if err := c.String(409, "custom response\n"); err != nil {
							t.Fatal(err)
						}
					}
					var state *observe.State
					var outerErr error
					e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
						return func(c *echo.Context) error {
							outerErr = next(c)
							if mode != "request-logger" {
								state = observe.Get(c.Request().Context())
							}
							return outerErr
						}
					})
					if mode == "request-logger" {
						e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
							HandleError: true, LogStatus: true,
							LogValuesFunc: func(c *echo.Context, values middleware.RequestLoggerValues) error {
								state = &observe.State{Error: values.Error, Status: values.Status}
								return nil
							},
						}))
					} else if mode != "baseline" {
						e.Use(Middleware(mode == "original"))
					}
					if !tc.missing {
						e.GET("/", func(c *echo.Context) error { return tc.err })
					}
					response := httptest.NewRecorder()
					e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
					return response, state, outerErr, calls, writes
				}
				baseline, _, _, calls, writes := run("baseline")
				if calls != 1 || writes != 1 {
					t.Fatalf("baseline calls/writes = %d/%d", calls, writes)
				}
				wantStatus := tc.status
				if handler != "default" {
					wantStatus = 409
				}
				if baseline.Code != wantStatus {
					t.Fatalf("baseline status = %d, want %d", baseline.Code, wantStatus)
				}
				for _, mode := range []string{"nil", "original", "request-logger"} {
					t.Run(mode, func(t *testing.T) {
						response, state, outerErr, calls, writes := run(mode)
						propagates := mode != "nil"
						wantCalls, wantWrites := 1, 1
						wantBody := baseline.Body.String()
						if propagates {
							wantCalls = 2
							if handler == "unguarded-custom" {
								wantWrites = 2
								wantBody += wantBody
							}
						}
						if calls != wantCalls || writes != wantWrites {
							t.Fatalf("calls/writes = %d/%d, want %d/%d", calls, writes, wantCalls, wantWrites)
						}
						if response.Code != baseline.Code || response.Body.String() != wantBody {
							t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), baseline.Code, wantBody)
						}
						if propagates && outerErr != tc.err || !propagates && outerErr != nil {
							t.Fatalf("outer error = %v", outerErr)
						}
						if state == nil || state.Error != tc.err || state.Status != response.Code || state.Stack != "" {
							t.Fatalf("state = %+v", state)
						}
					})
				}
			})
		}
	}
}

func TestValidationReturnedDirectly(t *testing.T) {
	for _, status := range []int{500, 400, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			run := func(observed bool) (*httptest.ResponseRecorder, *observe.State) {
				e := echo.New()
				v := validator.New()
				v.RegisterTagNameFunc(func(field reflect.StructField) string { return strings.Split(field.Tag.Get("json"), ",")[0] })
				e.Validator = structValidator{v}
				if status != 500 {
					defaultHandler := e.HTTPErrorHandler
					e.HTTPErrorHandler = func(c *echo.Context, err error) {
						var validation validator.ValidationErrors
						if errors.As(err, &validation) {
							if err := c.JSON(status, map[string]any{"message": "validation failed"}); err != nil {
								t.Fatal(err)
							}
							return
						}
						defaultHandler(c, err)
					}
				}
				if observed {
					e.Use(Middleware(false))
				}
				var state *observe.State
				e.POST("/", func(c *echo.Context) error {
					state = observe.Get(c.Request().Context())
					return c.Validate(validationInput{})
				})
				response := httptest.NewRecorder()
				e.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
				return response, state
			}
			baseline, _ := run(false)
			response, state := run(true)
			if response.Code != status || response.Code != baseline.Code || response.Body.String() != baseline.Body.String() {
				t.Fatalf("response differs: %d %q vs %d %q", response.Code, response.Body.String(), baseline.Code, baseline.Body.String())
			}
			if state == nil || state.Status != status || len(state.Validation) != 2 {
				t.Fatalf("state = %+v", state)
			}
			var validation validator.ValidationErrors
			if !errors.As(state.Error, &validation) {
				t.Fatalf("error = %T, want direct validator.ValidationErrors", state.Error)
			}
			for i, want := range []struct{ namespace, field, tag string }{{"validationInput.email", "email", "required"}, {"validationInput.address.city", "city", "required"}} {
				field := state.Validation[i]
				if field.Namespace != want.namespace || field.Field != want.field || field.Tag != want.tag || field.Message != validation[i].Error() {
					t.Fatalf("field = %+v, want %+v", field, want)
				}
			}
			// Recognition is independent of final-status eligibility (400/422 only).
			eligible := state.Status == 400 || state.Status == 422
			if eligible != (status != 500) {
				t.Fatalf("eligibility = %v", eligible)
			}
		})
	}
}

func TestRequestContextFirstErrorAndCancellation(t *testing.T) {
	first, returned := errors.New("explicit first"), errors.New("returned later")
	for _, tc := range []struct {
		name           string
		recorded       []error
		returned, want error
	}{
		{"first", []error{first}, returned, first},
		{"canceled", nil, context.Canceled, nil},
		{"wrapped-canceled", nil, fmt.Errorf("request: %w", context.Canceled), nil},
		{"canceled-then-error", []error{context.Canceled}, returned, returned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			e.Use(Middleware(false))
			var states []*observe.State
			e.GET("/", func(c *echo.Context) error {
				state := observe.Get(c.Request().Context())
				if state == nil || state.Error != nil {
					t.Fatalf("initial state = %+v", state)
				}
				states = append(states, state)
				for _, err := range tc.recorded {
					state.Record(err)
				}
				return tc.returned
			})
			for range 2 {
				response := httptest.NewRecorder()
				e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				state := states[len(states)-1]
				if state.Error != tc.want || state.Status != 500 || state.Stack != "" || response.Code != 500 {
					t.Fatalf("state = %+v, status = %d", state, response.Code)
				}
			}
			if states[0] == states[1] {
				t.Fatal("request state reused")
			}
		})
	}
}

func TestPanicWithOuterRecover(t *testing.T) {
	panicError := errors.New("panic error")
	panicPointer := &struct{ Message string }{"original value"}
	for _, value := range []any{"panic string", panicError, panicPointer} {
		for _, recovery := range []string{"default", "custom503", "partial202"} {
			t.Run(fmt.Sprintf("%T/%s", value, recovery), func(t *testing.T) {
				run := func(observed bool) (*httptest.ResponseRecorder, *observe.State, any, int) {
					e := echo.New()
					calls := 0
					defaultHandler := e.HTTPErrorHandler
					e.HTTPErrorHandler = func(c *echo.Context, err error) {
						calls++
						if recovery == "custom503" {
							if err := c.String(503, "custom recovery\n"); err != nil {
								t.Fatal(err)
							}
						} else {
							defaultHandler(c, err)
						}
					}
					e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{DisablePrintStack: true}))
					var repanicked any
					e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
						return func(c *echo.Context) error {
							defer func() {
								if v := recover(); v != nil {
									repanicked = v
									panic(v)
								}
							}()
							return next(c)
						}
					})
					if observed {
						e.Use(Middleware(false))
					}
					var state *observe.State
					e.GET("/", func(c *echo.Context) error {
						state = observe.Get(c.Request().Context())
						if recovery == "partial202" {
							if err := c.String(202, "partial\n"); err != nil {
								t.Fatal(err)
							}
						}
						panicEndpoint(value)
						return nil
					})
					response := httptest.NewRecorder()
					e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
					return response, state, repanicked, calls
				}
				baseline, _, _, _ := run(false)
				response, state, repanicked, calls := run(true)
				wantStatus, wantBody, seenStatus := 500, "{\"message\":\"Internal Server Error\"}\n", 200
				if recovery == "custom503" {
					wantStatus, wantBody = 503, "custom recovery\n"
				}
				if recovery == "partial202" {
					wantStatus, wantBody, seenStatus = 202, "partial\n", 202
				}
				if response.Code != wantStatus || response.Body.String() != wantBody || response.Code != baseline.Code || response.Body.String() != baseline.Body.String() {
					t.Fatalf("response = %d %q; baseline = %d %q", response.Code, response.Body.String(), baseline.Code, baseline.Body.String())
				}
				if state == nil || state.Panic != value || repanicked != value || state.Status != seenStatus || calls != 1 {
					t.Fatalf("state = %+v, repanic = %#v, calls = %d", state, repanicked, calls)
				}
				if err, ok := value.(error); ok {
					if state.Error != err {
						t.Fatal("panic error identity changed")
					}
				} else if state.Error == nil || state.Error.Error() != fmt.Sprint(value) {
					t.Fatalf("panic error = %v", state.Error)
				}
				if !strings.Contains(state.Stack, "panicEndpoint\n") || !strings.Contains(state.Stack, "middleware_test.go:") || strings.Contains(state.Stack, "goroutine ") || strings.Contains(state.Stack, "+0x") || strings.Contains(state.Stack, "observe.(*State).Recover") {
					t.Fatalf("unexpected stack: %s", state.Stack)
				}
				if len(state.RawFunctions) == 0 {
					t.Fatal("no raw stack frames")
				}
				// This is deliberately not forced to 500: outer recovery owns the wire response.
				if recovery != "partial202" && state.Status == response.Code {
					t.Fatal("observer unexpectedly saw outer recovery status")
				}
			})
		}
	}
}

func TestIgnoredPanicsPreserveIdentity(t *testing.T) {
	for _, value := range []error{http.ErrAbortHandler, context.Canceled, fmt.Errorf("request: %w", context.Canceled)} {
		t.Run(value.Error(), func(t *testing.T) {
			run := func(observed bool) (*httptest.ResponseRecorder, *observe.State, any, any, int) {
				e := echo.New()
				calls := 0
				defaultHandler := e.HTTPErrorHandler
				e.HTTPErrorHandler = func(c *echo.Context, err error) { calls++; defaultHandler(c, err) }
				e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{DisablePrintStack: true}))
				var repanicked any
				e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c *echo.Context) error {
						defer func() {
							if v := recover(); v != nil {
								repanicked = v
								panic(v)
							}
						}()
						return next(c)
					}
				})
				if observed {
					e.Use(Middleware(false))
				}
				var state *observe.State
				e.GET("/", func(c *echo.Context) error { state = observe.Get(c.Request().Context()); panic(value) })
				response := httptest.NewRecorder()
				var escaped any
				func() {
					defer func() { escaped = recover() }()
					e.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				}()
				return response, state, repanicked, escaped, calls
			}
			baseline, _, _, _, _ := run(false)
			response, state, repanicked, escaped, calls := run(true)
			wantCalls := 1
			var wantEscaped any
			if value == http.ErrAbortHandler {
				wantCalls = 0
				wantEscaped = value
			}
			if repanicked != value || escaped != wantEscaped || calls != wantCalls {
				t.Fatalf("repanic = %v, escaped = %v, calls = %d", repanicked, escaped, calls)
			}
			if state == nil || state.Error != nil || state.Panic != nil || state.Stack != "" || len(state.RawFunctions) != 0 || state.Status != 200 {
				t.Fatalf("state = %+v", state)
			}
			if response.Code != baseline.Code || response.Body.String() != baseline.Body.String() {
				t.Fatalf("response differs: %d %q vs %d %q", response.Code, response.Body.String(), baseline.Code, baseline.Body.String())
			}
		})
	}
}

type structValidator struct{ validator *validator.Validate }

func (v structValidator) Validate(value any) error { return v.validator.Struct(value) }

type validationInput struct {
	Email   string `json:"email" validate:"required"`
	Address struct {
		City string `json:"city" validate:"required"`
	} `json:"address"`
}

func panicEndpoint(value any) { panic(value) }
