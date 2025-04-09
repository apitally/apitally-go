package gin

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSetup(t *testing.T) {
	t.Run("GetRoutes", func(t *testing.T) {
		r := gin.New()

		r.GET("/hello", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Hello, World!",
			})
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
		assert.NotEmpty(t, versions["gin"])
		assert.Equal(t, appVersion, versions["app"])
	})
}
