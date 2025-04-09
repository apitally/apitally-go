package gin

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
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

			// Capture error from panic if any
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

			// Get consumer info if available
			var consumerIdentifier string
			if c, exists := c.Get("ApitallyConsumer"); exists {
				if consumer := internal.ConsumerFromStringOrObject(c); consumer != nil {
					consumerIdentifier = consumer.Identifier
					client.ConsumerRegistry.AddOrUpdateConsumer(consumer)
				}
			}

			// Determine response size
			responseSize := parseContentLength(c.Writer.Header().Get("Content-Length"))
			if responseSize == -1 {
				responseSize = int64(c.Writer.Size())
			}

			// Count request
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

				// Count validation errors if any
				if valErrValue, exists := c.Get("ApitallyValidationErrors"); exists && valErrValue != nil {
					validationErrors, ok := valErrValue.(validator.ValidationErrors)
					if ok {
						for _, fieldError := range validationErrors {
							client.ValidationErrorCounter.AddValidationError(
								consumerIdentifier,
								c.Request.Method,
								routePattern,
								fieldError.Field(),
								truncateValidationErrorMessage(fieldError.Error()),
								fieldError.Tag(),
							)
						}
					}
				}

				// Count server error if any
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
					Consumer:  &consumerIdentifier,
					Method:    c.Request.Method,
					URL:       getFullURL(c.Request),
					Headers:   transformHeaders(c.Request.Header),
					Body:      requestBody,
				}
				if routePattern != "" {
					request.Path = routePattern
				}
				if requestSize >= 0 {
					request.Size = &requestSize
				}
				response := common.Response{
					StatusCode:   statusCode,
					ResponseTime: float64(duration.Milliseconds()) / 1000.0,
					Headers:      transformHeaders(c.Writer.Header()),
					Body:         responseBody.Bytes(),
				}
				if responseSize >= 0 {
					response.Size = &responseSize
				}
				client.RequestLogger.LogRequest(&request, &response, recoveredErr, stackTrace)
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

func CaptureValidationError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	validationErrors, ok := err.(validator.ValidationErrors)
	if !ok {
		return
	}

	// Store validation errors in the context for middleware
	c.Set("ApitallyValidationErrors", validationErrors)
}

func truncateValidationErrorMessage(msg string) string {
	re := regexp.MustCompile(`^Key: '.+' Error:(.+)$`)
	matches := re.FindStringSubmatch(msg)
	if len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	return msg
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
