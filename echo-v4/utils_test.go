package apitally

import (
	"net/http"
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
		assert.NotEmpty(t, versions["echo"])
		assert.Equal(t, appVersion, versions["app"])
	})
}
