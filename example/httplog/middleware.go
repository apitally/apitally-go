package httplog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
)

// Custom ResponseWriter that captures the response body
type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w responseBodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Read and store the request body
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// Create a buffer to store the response body and a custom ResponseWriter
		responseBody := &bytes.Buffer{}
		writer := &responseBodyWriter{
			ResponseWriter: c.Writer,
			body:           responseBody,
		}
		c.Writer = writer

		c.Next()

		duration := time.Since(start)
		requestSize := len(requestBody)
		responseSize := c.Writer.Size()

		logEntry := gin.H{
			"timestamp":          float64(time.Now().UnixMilli()) / 1000.0,
			"method":             c.Request.Method,
			"url":                c.Request.URL.String(),
			"path":               c.FullPath(),
			"status":             c.Writer.Status(),
			"response_time":      duration.Seconds(),
			"request_headers":    c.Request.Header,
			"response_headers":   c.Writer.Header(),
			"request_body_size":  requestSize,
			"response_body_size": responseSize,
		}

		// Add request payload if present
		if len(requestBody) > 0 {
			logEntry["request_payload"] = string(requestBody)
		}

		// Add response payload if present
		if responseBody.Len() > 0 {
			logEntry["response_payload"] = responseBody.String()
		}

		// Log the entry as JSON
		logJSON, _ := json.MarshalIndent(logEntry, "", "  ")
		fmt.Println(string(logJSON))
	}
}
