package apitally

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestUtils(t *testing.T) {
	t.Run("GetRoutes", func(t *testing.T) {
		r := chi.NewRouter()

		r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello, World!"))
		})

		routes := getRoutes(r)
		assert.Equal(t, 1, len(routes))
		assert.Equal(t, "GET", routes[0].Method)
		assert.Equal(t, "/hello", routes[0].Path)
	})

	t.Run("GetVersions", func(t *testing.T) {
		appVersion := "1.0.0"
		versions := getVersions(appVersion)
		assert.NotEmpty(t, versions["go"])
		assert.NotEmpty(t, versions["chi"])
		assert.Equal(t, appVersion, versions["app"])
	})

	t.Run("GetFullURL", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?q=1", nil)
		req.Host = "example.com"
		assert.Equal(t, "http://example.com/test?q=1", getFullURL(req))

		req.Header.Set("X-Forwarded-Proto", "https")
		assert.Equal(t, "https://example.com/test?q=1", getFullURL(req))
	})

	t.Run("ParseContentLength", func(t *testing.T) {
		assert.Equal(t, int64(-1), parseContentLength(""))
		assert.Equal(t, int64(-1), parseContentLength("invalid"))
		assert.Equal(t, int64(123), parseContentLength("123"))
	})

	t.Run("TransformHeaders", func(t *testing.T) {
		header := http.Header{}
		header.Add("Content-Type", "application/json")
		header.Add("Accept", "application/json")
		header.Add("Accept", "text/plain")

		headers := transformHeaders(header)
		assert.Equal(t, 3, len(headers))
		assert.Contains(t, headers, [2]string{"Content-Type", "application/json"})
		assert.Contains(t, headers, [2]string{"Accept", "application/json"})
		assert.Contains(t, headers, [2]string{"Accept", "text/plain"})
	})

	t.Run("GetRoutePattern", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {})

		req := httptest.NewRequest("GET", "/users/123", nil)
		rctx := chi.NewRouteContext()
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		// Without route pattern
		assert.Equal(t, "", getRoutePattern(req))

		// With route pattern
		rctx.RoutePatterns = []string{"/users/{id}"}
		assert.Equal(t, "/users/{id}", getRoutePattern(req))

		// Without Chi context
		req = httptest.NewRequest("GET", "/users/123", nil)
		assert.Equal(t, "", getRoutePattern(req))
	})

	t.Run("TruncateValidationErrorMessage", func(t *testing.T) {
		msg := "Key: 'User.Name' Error: required field"
		assert.Equal(t, "required field", truncateValidationErrorMessage(msg))

		msg = "some other error"
		assert.Equal(t, msg, truncateValidationErrorMessage(msg))
	})
}
