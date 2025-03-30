// Package httplog provides HTTP request and response logging middleware
package httplog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
)

// responseBodyWriter is a custom ResponseWriter that captures the response body
type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w responseBodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// RequestLogger middleware logs detailed information about requests and responses
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start time
		start := time.Now()

		// Read and store the request body
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			// Restore the request body for later use
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// Create a buffer to store the response body
		responseBody := &bytes.Buffer{}
		// Create a custom ResponseWriter
		writer := &responseBodyWriter{
			ResponseWriter: c.Writer,
			body:           responseBody,
		}
		c.Writer = writer

		// Process request
		c.Next()

		// Calculate duration
		duration := time.Since(start)

		// Get request body size
		requestSize := len(requestBody)

		// Get response body size
		responseSize := c.Writer.Size()

		// Create log entry
		logEntry := gin.H{
			"timestamp":          time.Now().Format(time.RFC3339),
			"method":             c.Request.Method,
			"url":                c.Request.URL.String(),
			"route_pattern":      c.FullPath(),
			"status":             c.Writer.Status(),
			"duration":           duration.String(),
			"request_headers":    c.Request.Header,
			"response_headers":   c.Writer.Header(),
			"request_body_size":  requestSize,
			"response_body_size": responseSize,
		}

		// Add request payload if present
		if len(requestBody) > 0 {
			var prettyRequest bytes.Buffer
			if json.Indent(&prettyRequest, requestBody, "", "  ") == nil {
				logEntry["request_payload"] = prettyRequest.String()
			} else {
				logEntry["request_payload"] = string(requestBody)
			}
		}

		// Add response payload if present
		if responseBody.Len() > 0 {
			var prettyResponse bytes.Buffer
			if json.Indent(&prettyResponse, responseBody.Bytes(), "", "  ") == nil {
				logEntry["response_payload"] = prettyResponse.String()
			} else {
				logEntry["response_payload"] = responseBody.String()
			}
		}

		// Log the entry as JSON
		logJSON, _ := json.MarshalIndent(logEntry, "", "  ")
		fmt.Println(string(logJSON))
	}
}
