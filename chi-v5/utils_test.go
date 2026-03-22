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
		assert.Equal(t, appVersion, versions["app"])
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
}
