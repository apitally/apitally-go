package apitally

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestApp(requestLoggingEnabled bool) *gin.Engine {
	config := &common.Config{
		ClientID: "e117eb33-f6d2-4260-a71d-31eb49425893",
		Env:      "test",
		RequestLogging: &common.RequestLoggingConfig{
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

	r := gin.Default()
	r.Use(Middleware(r, config))

	r.GET("/hello", func(c *gin.Context) {
		SetConsumerIdentifier(c, "tester")
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, World!",
		})
	})

	r.POST("/hello", func(c *gin.Context) {
		SetConsumer(c, common.Consumer{
			Identifier: "tester",
			Name:       "Tester",
			Group:      "Test Group",
		})
		var req struct {
			Name string `json:"name" binding:"required,min=3"`
		}
		if err := c.BindJSON(&req); err != nil {
			CaptureValidationError(c, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		time.Sleep(100 * time.Millisecond)
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, " + req.Name + "!",
		})
	})

	r.GET("/error", func(c *gin.Context) {
		panic("test panic")
	})

	return r
}

func TestMiddleware(t *testing.T) {
	t.Run("RequestCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		r := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/hello", nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		req, _ = http.NewRequest("POST", "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/error", nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)

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
		r := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/hello", bytes.NewBuffer([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)

		w = httptest.NewRecorder()
		req, _ = http.NewRequest("POST", "/hello", bytes.NewBuffer([]byte(`{"name": "x"}`)))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)

		validationErrors := c.ValidationErrorCounter.GetAndResetValidationErrors()
		assert.Len(t, validationErrors, 2)

		assert.True(t, slices.ContainsFunc(validationErrors, func(r internal.ValidationErrorsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				slices.Equal(r.Loc, []string{"Name"}) &&
				r.Msg == "Field validation for 'Name' failed on the 'required' tag" &&
				r.Type == "required"
		}))
		assert.True(t, slices.ContainsFunc(validationErrors, func(r internal.ValidationErrorsItem) bool {
			return r.Consumer == "tester" &&
				r.Method == "POST" &&
				r.Path == "/hello" &&
				slices.Equal(r.Loc, []string{"Name"}) &&
				r.Msg == "Field validation for 'Name' failed on the 'min' tag" &&
				r.Type == "min"
		}))
	})

	t.Run("ServerErrorCounter", func(t *testing.T) {
		internal.ResetApitallyClient()
		r := setupTestApp(false)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/error", nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)

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
		r := setupTestApp(true)
		c := internal.GetApitallyClient()
		defer c.Shutdown()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/hello", bytes.NewBuffer([]byte(`{"name": "John"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "16")
		req.Host = "example.com"
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/error", nil)
		req.Host = "example.com"
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)

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
		assert.Equal(t, "application/json; charset=utf-8", respHeaders[0][1])

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
