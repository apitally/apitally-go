package apitally

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/apitally/apitally-go/internal"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

func setupTestApp(requestLoggingEnabled bool) *echo.Echo {
	config := NewConfig("e117eb33-f6d2-4260-a71d-31eb49425893")
	config.Env = "test"
	config.RequestLogging.Enabled = requestLoggingEnabled
	config.RequestLogging.LogRequestHeaders = true
	config.RequestLogging.LogRequestBody = true
	config.RequestLogging.LogResponseBody = true
	config.RequestLogging.CaptureSpans = true
	config.DisableSync = true

	e := echo.New()
	e.Use(middleware.Recover())
	e.Use(Middleware(e, config))

	e.GET("/hello", func(c echo.Context) error {
		SetConsumerIdentifier(c, "tester")
		return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
	})

	e.POST("/hello", func(c echo.Context) error {
		SetConsumer(c, Consumer{
			Identifier: "tester",
			Name:       "Tester",
			Group:      "Test Group",
		})

		var req struct {
			Name string `json:"name" validate:"required,min=3"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		validate := validator.New()
		if err := validate.Struct(req); err != nil {
			CaptureValidationError(c, err)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		_, span := otel.Tracer("test").Start(c.Request().Context(), "child-span")
		time.Sleep(100 * time.Millisecond)
		span.End()

		return c.JSON(http.StatusOK, map[string]string{"message": "Hello, " + req.Name + "!"})
	})

	e.GET("/error", func(c echo.Context) error {
		panic("test panic")
	})

	return e
}

func TestMiddleware(t *testing.T) {
	t.Run("RequestCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		e := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodGet, "/hello", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		req = httptest.NewRequest(http.MethodGet, "/error", nil)
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		requests := c.RequestCounter.GetAndResetRequests()
		assert.Len(t, requests, 3)

		assert.True(t, slices.ContainsFunc(requests, func(r internal.RequestsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "GET" &&
				r.Path == "/hello" &&
				r.StatusCode == http.StatusOK &&
				r.RequestSizeSum == int64(0) &&
				r.ResponseSizeSum > int64(0)
		}))
		assert.True(t, slices.ContainsFunc(requests, func(r internal.RequestsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				r.StatusCode == http.StatusOK &&
				r.RequestSizeSum == int64(16)
		}))
		assert.True(t, slices.ContainsFunc(requests, func(r internal.RequestsItem) bool {
			return r.Method == "GET" &&
				r.Path == "/error" &&
				r.StatusCode == http.StatusInternalServerError
		}))
	})

	t.Run("ValidationErrorCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		e := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "x"}`)))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		validationErrors := c.ValidationErrorCounter.GetAndResetValidationErrors()
		assert.Len(t, validationErrors, 2)

		assert.True(t, slices.ContainsFunc(validationErrors, func(r internal.ValidationErrorsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				len(r.Loc) == 1 && r.Loc[0] == "Name" &&
				r.Msg == "Field validation for 'Name' failed on the 'required' tag" &&
				r.Type == "required"
		}))
		assert.True(t, slices.ContainsFunc(validationErrors, func(r internal.ValidationErrorsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				len(r.Loc) == 1 && r.Loc[0] == "Name" &&
				r.Msg == "Field validation for 'Name' failed on the 'min' tag" &&
				r.Type == "min"
		}))
	})

	t.Run("ServerErrorCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		e := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodGet, "/error", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		errors := c.ServerErrorCounter.GetAndResetServerErrors()
		assert.Len(t, errors, 1)

		assert.Equal(t, "GET", errors[0].Method)
		assert.Equal(t, "/error", errors[0].Path)
		assert.Equal(t, "errors.errorString", errors[0].Type)
		assert.Equal(t, "test panic", errors[0].Message)
		assert.Contains(t, errors[0].StackTrace, "panic")
	})

	t.Run("RequestLogger", func(t *testing.T) {
		internal.ResetApitallyClient()
		e := setupTestApp(true)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		req = httptest.NewRequest(http.MethodGet, "/error", nil)
		req.Host = "example.com"
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		logItems := c.RequestLogger.GetPendingWrites()
		assert.Len(t, logItems, 2)

		// Validate log item for POST /hello request
		helloLogItem := logItems[0]
		assert.Equal(t, "tester", helloLogItem.Request.Consumer)
		assert.Equal(t, "POST", helloLogItem.Request.Method)
		assert.Equal(t, "/hello", helloLogItem.Request.Path)
		assert.Equal(t, "http://example.com/hello", helloLogItem.Request.URL)
		assert.Equal(t, 200, helloLogItem.Response.StatusCode)
		assert.GreaterOrEqual(t, helloLogItem.Response.ResponseTime, 0.1)
		assert.Contains(t, string(helloLogItem.Request.Body), "John")
		assert.Contains(t, string(helloLogItem.Response.Body), "Hello, John!")
		assert.Equal(t, int64(16), helloLogItem.Request.Size)
		assert.Equal(t, int64(27), helloLogItem.Response.Size)
		assert.Nil(t, helloLogItem.Exception)

		reqHeaders := helloLogItem.Request.Headers
		var contentType, contentLength string
		for _, h := range reqHeaders {
			if h[0] == "Content-Type" {
				contentType = h[1]
			}
			if h[0] == "Content-Length" {
				contentLength = h[1]
			}
		}
		assert.Equal(t, "application/json", contentType)
		assert.Equal(t, "16", contentLength)

		respHeaders := helloLogItem.Response.Headers
		assert.Len(t, respHeaders, 1)
		assert.Equal(t, "Content-Type", respHeaders[0][0])
		assert.Contains(t, respHeaders[0][1], "application/json")

		// Validate spans are logged
		assert.Len(t, helloLogItem.TraceID, 32)
		assert.Len(t, helloLogItem.Spans, 2)
		spanNames := []string{helloLogItem.Spans[0].Name, helloLogItem.Spans[1].Name}
		assert.Contains(t, spanNames, "POST /hello")
		assert.Contains(t, spanNames, "child-span")

		// Validate log item for GET /error request
		errorLogItem := logItems[1]
		assert.Equal(t, "GET", errorLogItem.Request.Method)
		assert.Equal(t, "/error", errorLogItem.Request.Path)
		assert.Equal(t, 500, errorLogItem.Response.StatusCode)
		assert.NotNil(t, errorLogItem.Exception)
		assert.Equal(t, "errors.errorString", errorLogItem.Exception.Type)
		assert.Equal(t, "test panic", errorLogItem.Exception.Message)
		assert.Contains(t, errorLogItem.Exception.StackTrace, "panic")
	})
}
