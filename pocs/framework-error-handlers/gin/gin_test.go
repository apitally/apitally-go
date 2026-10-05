package gin_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

func observer(done chan<- *observe.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := &observe.State{}
		c.Request = c.Request.WithContext(observe.Put(c.Request.Context(), state))
		defer func() { done <- state }()
		defer func() { state.Status = c.Writer.Status() }()
		defer state.Recover()
		c.Next()
		if len(c.Errors) > 0 {
			state.Record(c.Errors[0].Err)
		}
	}
}

func TestErrorChannel(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	for _, test := range []struct {
		name    string
		handler gin.HandlerFunc
		want    error
		status  int
	}{
		{"plain", func(c *gin.Context) { c.Error(first); c.Status(500) }, first, 500},
		{"abort", func(c *gin.Context) { c.AbortWithError(500, first) }, first, 500},
		{"first", func(c *gin.Context) { c.Error(first); c.Error(second); c.Status(500) }, first, 500},
		{"canceled", func(c *gin.Context) { c.Error(context.Canceled); c.Status(500) }, nil, 500},
		{"wrapped-canceled", func(c *gin.Context) { c.Error(errors.Join(context.Canceled, first)); c.Status(500) }, nil, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan *observe.State, 1)
			r := gin.New()
			r.Use(observer(done))
			r.GET("/", test.handler)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			state := <-done
			if state.Error != test.want || state.Status != test.status || response.Code != test.status {
				t.Fatalf("error=%v observed=%d response=%d", state.Error, state.Status, response.Code)
			}
			if state.Stack != "" || state.Panic != nil {
				t.Fatalf("ordinary error acquired panic state: %+v", state)
			}
		})
	}
}

func TestBindingVisibility(t *testing.T) {
	type Input struct {
		Name string `json:"name" binding:"required"`
	}
	for _, automatic := range []bool{true, false} {
		name := "ShouldBindJSON"
		if automatic {
			name = "BindJSON"
		}
		t.Run(name, func(t *testing.T) {
			done := make(chan *observe.State, 1)
			r := gin.New()
			r.Use(observer(done))
			var bindingError error
			r.POST("/", func(c *gin.Context) {
				var input Input
				if automatic {
					bindingError = c.BindJSON(&input)
				} else {
					bindingError = c.ShouldBindJSON(&input)
					c.Status(400)
				}
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(response, request)
			state := <-done
			var fields validator.ValidationErrors
			if !errors.As(bindingError, &fields) || len(fields) != 1 {
				t.Fatalf("not a real go-playground validation failure: %v", bindingError)
			}
			if response.Code != 400 || state.Status != 400 {
				t.Fatalf("response=%d observed=%d", response.Code, state.Status)
			}
			if automatic {
				if state.Error == nil || state.Error.Error() != bindingError.Error() || len(state.Validation) != 1 {
					t.Fatalf("missing automatic validation: %+v", state)
				}
				field := state.Validation[0]
				if field.Namespace != "Input.Name" || field.Field != "Name" || field.Tag != "required" || field.Message != fields[0].Error() {
					t.Fatalf("unexpected validation: %+v", field)
				}
			} else if state.Error != nil || len(state.Validation) != 0 {
				t.Fatalf("ShouldBindJSON exposed an error to middleware: %+v", state)
			}
		})
	}
}

func TestContextFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "disabled"
		if fallback {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			done := make(chan *observe.State, 1)
			r := gin.New()
			r.ContextWithFallback = fallback
			r.Use(observer(done))
			var requestState, ginState *observe.State
			r.GET("/", func(c *gin.Context) {
				requestState = observe.Get(c.Request.Context())
				ginState = observe.Get(c)
				// Framework helpers must use Request.Context even with fallback off.
				observe.Get(c.Request.Context()).Record(errors.New("explicit"))
			})
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			state := <-done
			if requestState != state || state.Error == nil || (fallback && ginState != state) || (!fallback && ginState != nil) {
				t.Fatalf("fallback=%t request=%p gin=%p state=%p", fallback, requestState, ginState, state)
			}
		})
	}
}

func TestOuterRecovery(t *testing.T) {
	oldOutput, oldErrorOutput := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	defer func() { gin.DefaultWriter, gin.DefaultErrorWriter = oldOutput, oldErrorOutput }()
	value := &struct{ Message string }{"original panic"}
	for _, test := range []struct {
		name     string
		custom   bool
		partial  bool
		final    int
		observed int
		abort    bool
	}{
		{"Default", false, false, 500, 200, false},
		{"partial-write", false, true, 202, 202, false},
		{"custom-status", true, false, 418, 200, false},
		{"ErrAbortHandler", false, false, 200, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var baseline *httptest.ResponseRecorder
			var panicValue any = value
			if test.abort {
				panicValue = http.ErrAbortHandler
			}
			for _, instrumented := range []bool{false, true} {
				done := make(chan *observe.State, 1)
				var r *gin.Engine
				if test.custom {
					r = gin.New()
					r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) { c.AbortWithStatus(418) }))
				} else {
					r = gin.Default()
				}
				var repanicked any
				r.Use(func(c *gin.Context) {
					defer func() {
						if v := recover(); v != nil {
							repanicked = v
							panic(v)
						}
					}()
					c.Next()
				})
				if instrumented {
					r.Use(observer(done))
				}
				r.GET("/", func(c *gin.Context) {
					if test.partial {
						c.String(202, "partial")
					}
					ginPanic(panicValue)
				})
				response := httptest.NewRecorder()
				r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				if repanicked != panicValue || response.Code != test.final {
					t.Fatalf("panic=%v response=%d", repanicked, response.Code)
				}
				if !instrumented {
					baseline = response
					continue
				}
				if response.Code != baseline.Code || response.Body.String() != baseline.Body.String() {
					t.Fatal("observer changed the client response")
				}
				state := <-done
				if test.abort {
					if state.Panic != nil || state.Error != nil || state.Stack != "" || len(state.RawFunctions) != 0 || state.Status != test.observed {
						t.Fatalf("abort panic was captured: %+v", state)
					}
					continue
				}
				if state.Panic != panicValue || state.Error == nil || state.Status != test.observed || !strings.Contains(state.Stack, ".ginPanic\n") {
					t.Fatalf("unexpected panic observation: %+v", state)
				}
				if len(state.RawFunctions) == 0 || strings.Contains(state.Stack, "goroutine ") || strings.Contains(state.Stack, "+0x") {
					t.Fatalf("unexpected stack format: %s", state.Stack)
				}
			}
		})
	}
}

func ginPanic(value any) { panic(value) }
