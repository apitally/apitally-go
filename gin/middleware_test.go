package gin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestApp(t *testing.T) (*gin.Engine, *internal.ApitallyClient) {
	config := &common.ApitallyConfig{
		ClientId: "e117eb33-f6d2-4260-a71d-31eb49425893",
		Env:      "test",
		RequestLoggingConfig: &common.RequestLoggingConfig{
			Enabled:            true,
			LogQueryParams:     true,
			LogRequestHeaders:  true,
			LogRequestBody:     true,
			LogResponseHeaders: true,
			LogResponseBody:    true,
			LogPanic:           true,
		},
	}
	client, err := internal.NewApitallyClient(*config)
	assert.NoError(t, err)

	r := gin.Default()
	r.Use(ApitallyMiddleware(client))

	r.GET("/hello", func(c *gin.Context) {
		c.Set("ApitallyConsumer", "tester")
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, World!",
		})
	})

	r.POST("/hello", func(c *gin.Context) {
		c.Set("ApitallyConsumer", "tester")
		var req struct {
			Name string `json:"name"`
		}
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, " + req.Name + "!",
		})
	})

	r.GET("/error", func(c *gin.Context) {
		panic("test panic")
	})

	return r, client
}

func TestMiddleware(t *testing.T) {
	t.Run("RequestCounter", func(t *testing.T) {
		r, c := setupTestApp(t)
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

	t.Run("ServerErrorCounter", func(t *testing.T) {
		r, c := setupTestApp(t)
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
}
