package apitally

import (
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestUtils(t *testing.T) {
	t.Run("GetRoutes", func(t *testing.T) {
		app := fiber.New()

		app.Get("/hello", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{
				"message": "Hello, World!",
			})
		})

		routes := getRoutes(app)
		assert.Equal(t, 1, len(routes))
		assert.Equal(t, "GET", routes[0].Method)
		assert.Equal(t, "/hello", routes[0].Path)
	})

	t.Run("GetVersions", func(t *testing.T) {
		appVersion := "1.0.0"
		versions := getVersions(appVersion)
		assert.NotEmpty(t, versions["go"])
		assert.NotEmpty(t, versions["fiber"])
		assert.Equal(t, appVersion, versions["app"])
	})
}
