package gin

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
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

		// Get route pattern
		routePattern := c.FullPath()

		// Get consumer info if available
		var consumerIdentifier string
		if c, exists := c.Get("ApitallyConsumer"); exists {
			if consumer := internal.ConsumerFromStringOrObject(c); consumer != nil {
				consumerIdentifier = consumer.Identifier
				client.ConsumerRegistry.AddOrUpdateConsumer(consumer)
			}
		}

		// Determine request size
		requestSize := parseContentLength(c.Request.Header.Get("Content-Length"))

		// Cache request body if needed
		var requestBody []byte
		if client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogRequestBody &&
			c.Request.Body != nil {
			var err error
			requestBody, err = io.ReadAll(c.Request.Body)
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
				if requestSize == -1 {
					requestSize = int64(len(requestBody))
				}
			}
		}

		// Prepare response writer to capture body if needed
		var responseBody bytes.Buffer
		var originalWriter gin.ResponseWriter
		if client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogResponseBody {
			originalWriter = c.Writer
			c.Writer = &responseBodyWriter{
				ResponseWriter: c.Writer,
				body:           &responseBody,
			}
		}

		start := time.Now()

		defer func() {
			duration := time.Since(start)
			statusCode := c.Writer.Status()

			var panicValue any
			var recoveredErr error
			var stackTrace string
			if r := recover(); r != nil {
				panicValue = r
				statusCode = http.StatusInternalServerError
				stackTrace = string(debug.Stack())
				if err, ok := r.(error); ok {
					recoveredErr = err
				} else {
					recoveredErr = fmt.Errorf("%v", r)
				}
			}

			// Determine response size
			responseSize := parseContentLength(c.Writer.Header().Get("Content-Length"))
			if responseSize == -1 {
				responseSize = int64(c.Writer.Size())
			}

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

				// Track server error if any
				if recoveredErr != nil {
					client.ServerErrorCounter.AddServerError(
						consumerIdentifier,
						c.Request.Method,
						routePattern,
						recoveredErr,
						stackTrace,
					)
				}
			}

			// Log request if enabled
			if client.Config.RequestLoggingConfig != nil && client.Config.RequestLoggingConfig.Enabled {
				request := common.Request{
					Timestamp: float64(time.Now().UnixMilli()) / 1000.0,
					Method:    c.Request.Method,
					Path:      routePattern,
					URL:       getFullURL(c.Request),
					Headers:   transformHeaders(c.Request.Header),
					Size:      &requestSize,
					Consumer:  &consumerIdentifier,
					Body:      requestBody,
				}
				response := common.Response{
					StatusCode:   statusCode,
					ResponseTime: float64(duration.Milliseconds()) / 1000.0,
					Headers:      transformHeaders(c.Writer.Header()),
					Size:         &responseSize,
					Body:         responseBody.Bytes(),
				}
				client.RequestLogger.LogRequest(&request, &response, &recoveredErr, &stackTrace)
			}

			// Restore original writer if needed
			if originalWriter != nil {
				c.Writer = originalWriter
			}

			// Re-panic if there was a panic
			if panicValue != nil {
				panic(panicValue)
			}
		}()

		c.Next()
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

func parseContentLength(contentLength string) int64 {
	if contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			return size
		}
	}
	return -1
}

func transformHeaders(header http.Header) [][2]string {
	headers := make([][2]string, 0, len(header))
	for k, v := range header {
		if len(v) > 0 {
			headers = append(headers, [2]string{k, v[0]})
		}
	}
	return headers
}
