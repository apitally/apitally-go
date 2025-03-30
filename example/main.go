package main

import (
	"net/http"

	"example/httplog"

	"github.com/gin-gonic/gin"
)

func main() {
	// Create a default gin router
	r := gin.Default()

	// Add our custom request logger middleware
	r.Use(httplog.RequestLogger())

	// Define a route for GET /hello
	r.GET("/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, World!",
		})
	})

	// Run the server on port 8080
	r.Run(":8080")
}
