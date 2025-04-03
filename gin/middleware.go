package gin

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
)

type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w responseBodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func ApitallyMiddleware(client *internal.ApitallyClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !client.IsEnabled() {
			c.Next()
			return
		}

		start := time.Now()
		var requestBody []byte
		var responseBody bytes.Buffer
		var err error

		// Cache request body if needed
		if client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogRequestBody &&
			c.Request.Body != nil {
			requestBody, err = io.ReadAll(c.Request.Body)
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			}
		}

		// Prepare response writer to capture body if needed
		originalWriter := c.Writer
		shouldCaptureResponse := client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogResponseBody

		if shouldCaptureResponse {
			c.Writer = &responseBodyWriter{
				ResponseWriter: c.Writer,
				body:           &responseBody,
			}
		}

		duration := time.Since(start)
		statusCode := c.Writer.Status()
		requestSize := int64(len(requestBody))
		responseSize := int64(c.Writer.Size())

		// Get consumer info if available
		var consumerIdentifier string
		if consumer, exists := c.Get("ApitallyConsumer"); exists {
			if c, ok := consumer.(*common.ApitallyConsumer); ok {
				consumerIdentifier = c.Identifier
				client.ConsumerRegistry.AddOrUpdateConsumer(c)
			}
		}

		// Get route pattern
		routePattern := c.FullPath()

		// Track request
		if routePattern != "" {
			client.RequestCounter.AddRequest(
				consumerIdentifier,
				c.Request.Method,
				routePattern,
				statusCode,
				float64(duration.Milliseconds())/1000.0,
				requestSize,
				responseSize,
			)
		}

		// Log request if enabled
		if client.Config.RequestLoggingConfig != nil && client.Config.RequestLoggingConfig.Enabled {
			headers := make([][2]string, 0)
			for k, v := range c.Request.Header {
				if len(v) > 0 {
					headers = append(headers, [2]string{k, v[0]})
				}
			}

			responseHeaders := make([][2]string, 0)
			for k, v := range c.Writer.Header() {
				if len(v) > 0 {
					responseHeaders = append(responseHeaders, [2]string{k, v[0]})
				}
			}

			request := common.Request{
				Timestamp: float64(time.Now().UnixMilli()) / 1000.0,
				Method:    c.Request.Method,
				Path:      routePattern,
				URL:       getFullURL(c.Request),
				Headers:   headers,
				Size:      &requestSize,
				Consumer:  &consumerIdentifier,
			}

			if client.Config.RequestLoggingConfig.LogRequestBody {
				request.Body = requestBody
			}

			response := common.Response{
				StatusCode:   statusCode,
				ResponseTime: float64(duration.Milliseconds()) / 1000.0,
				Headers:      responseHeaders,
				Size:         &responseSize,
			}

			if client.Config.RequestLoggingConfig.LogResponseBody {
				response.Body = responseBody.Bytes()
			}

			client.RequestLogger.LogRequest(&request, &response)
		}

		// Restore original writer if needed
		if shouldCaptureResponse {
			c.Writer = originalWriter
		}
	}
}

func getFullURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := req.Host
	if host == "" {
		host = req.Header.Get("Host")
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, req.URL.String())
}
