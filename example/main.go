package main

import (
	"net/http"

	"github.com/apitally/apitally-go/common"
	apitally "github.com/apitally/apitally-go/gin"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	config := &common.ApitallyConfig{
		ClientId: "54badc91-c693-4db8-9be1-8a281a79dac4",
		Env:      "dev",
	}
	apitally.UseApitally(r, config)

	r.GET("/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "Hello, World!",
		})
	})

	r.Run(":8083")
}
