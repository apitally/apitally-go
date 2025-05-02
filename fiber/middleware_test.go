package fiber

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apitally/apitally-go/internal"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/stretchr/testify/assert"
)

func setupTestApp(requestLoggingEnabled bool) *fiber.App {
	config := &ApitallyConfig{
		ClientId: "e117eb33-f6d2-4260-a71d-31eb49425893",
		Env:      "test",
		RequestLoggingConfig: &RequestLoggingConfig{
			Enabled:            requestLoggingEnabled,
			LogQueryParams:     true,
			LogRequestHeaders:  true,
			LogRequestBody:     true,
			LogResponseHeaders: true,
			LogResponseBody:    true,
			LogPanic:           true,
		},
		DisableSync: true,
	}

	app := fiber.New()
	app.Use(recover.New())
	app.Use(ApitallyMiddleware(app, config))

	app.Get("/hello", func(c *fiber.Ctx) error {
		c.Locals("ApitallyConsumer", "tester")
		return c.JSON(fiber.Map{"message": "Hello, World!"})
	})

	app.Post("/hello", func(c *fiber.Ctx) error {
		c.Locals("ApitallyConsumer", ApitallyConsumer{
			Identifier: "tester",
			Name:       "Tester",
			Group:      "Test Group",
		})

		var req struct {
			Name string `json:"name" validate:"required,min=3"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		validate := validator.New()
		if err := validate.Struct(req); err != nil {
			CaptureValidationError(c, err)
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		time.Sleep(100 * time.Millisecond)
		return c.JSON(fiber.Map{"message": "Hello, " + req.Name + "!"})
	})

	app.Get("/error", func(c *fiber.Ctx) error {
		panic("test panic")
	})

	return app
}

func TestMiddleware(t *testing.T) {
	t.Run("RequestCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		app := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodGet, "/hello", nil)
		resp, _ := app.Test(req)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		resp, _ = app.Test(req)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		req = httptest.NewRequest(http.MethodGet, "/error", nil)
		resp, _ = app.Test(req)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		requests := c.RequestCounter.GetAndResetRequests()
		assert.Len(t, requests, 3)

		assert.True(t, containsRequest(requests, func(r internal.RequestsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "GET" &&
				r.Path == "/hello" &&
				r.StatusCode == http.StatusOK &&
				r.RequestSizeSum == int64(0) &&
				r.ResponseSizeSum > int64(0)
		}))
		assert.True(t, containsRequest(requests, func(r internal.RequestsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				r.StatusCode == http.StatusOK &&
				r.RequestSizeSum == int64(16)
		}))
		assert.True(t, containsRequest(requests, func(r internal.RequestsItem) bool {
			return r.Method == "GET" &&
				r.Path == "/error" &&
				r.StatusCode == http.StatusInternalServerError
		}))
	})

	t.Run("ValidationErrorCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		app := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		req = httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "x"}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, _ = app.Test(req)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		validationErrors := c.ValidationErrorCounter.GetAndResetValidationErrors()
		assert.Len(t, validationErrors, 2)

		assert.True(t, containsValidationError(validationErrors, func(r internal.ValidationErrorsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				len(r.Loc) == 1 && r.Loc[0] == "Name" &&
				r.Msg == "Field validation for 'Name' failed on the 'required' tag" &&
				r.Type == "required"
		}))
		assert.True(t, containsValidationError(validationErrors, func(r internal.ValidationErrorsItem) bool {
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
		app := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodGet, "/error", nil)
		resp, _ := app.Test(req)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

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
		app := setupTestApp(true)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		req := httptest.NewRequest(http.MethodPost, "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		resp, _ := app.Test(req)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		req = httptest.NewRequest(http.MethodGet, "/error", nil)
		resp, _ = app.Test(req)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		pendingWrites := c.RequestLogger.GetPendingWrites()
		assert.Len(t, pendingWrites, 2)

		// Deserialize log items
		logItems := make([]internal.RequestLogItem, len(pendingWrites))
		for i, write := range pendingWrites {
			err := json.Unmarshal([]byte(write), &logItems[i])
			assert.NoError(t, err)
		}

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
		assert.Equal(t, int64(26), helloLogItem.Response.Size)
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
		assert.Equal(t, "application/json", respHeaders[0][1])

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

func containsRequest(requests []internal.RequestsItem, match func(internal.RequestsItem) bool) bool {
	for _, r := range requests {
		if match(r) {
			return true
		}
	}
	return false
}

func containsValidationError(errors []internal.ValidationErrorsItem, match func(internal.ValidationErrorsItem) bool) bool {
	for _, r := range errors {
		if match(r) {
			return true
		}
	}
	return false
}
