package apitally

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestUtils(t *testing.T) {
	t.Run("GetRoutes", func(t *testing.T) {
		e := echo.New()

		e.GET("/hello", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{
				"message": "Hello, World!",
			})
		})

		routes := getRoutes(e)
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
		e := echo.New()
		e.GET("/users/:id", func(c echo.Context) error {
			return nil
		})

		req := httptest.NewRequest("GET", "/users/123", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Set the path on the context
		c.SetPath("/users/:id")

		assert.Equal(t, "/users/:id", getRoutePattern(c))

		// Without path set
		c2 := e.NewContext(req, rec)
		assert.Equal(t, "", getRoutePattern(c2))
	})
}
