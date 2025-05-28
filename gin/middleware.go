package apitally

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type responseWriter struct {
	gin.ResponseWriter
	size                   int64
	body                   *bytes.Buffer
	shouldCaptureBody      *bool
	isSupportedContentType func(string) bool
	exceededMaxSize        bool
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.shouldCaptureBody == nil {
		w.shouldCaptureBody = new(bool)
		*w.shouldCaptureBody = w.isSupportedContentType(w.Header().Get("Content-Type"))
	}
	if *w.shouldCaptureBody && !w.exceededMaxSize {
		if w.body.Len()+len(b) <= common.MaxBodySize {
			w.body.Write(b)
		} else {
			w.body.Reset()
			w.exceededMaxSize = true
		}
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += int64(n)
	return n, err
}

func (w *responseWriter) Size() int {
	return int(w.size)
}

func Middleware(r *gin.Engine, config *Config) gin.HandlerFunc {
	client, err := internal.InitApitallyClient(*config)
	if err != nil {
		panic(err)
	}

	// Sync should only be disabled for testing purposes
	if !config.DisableSync {
		client.StartSync()

		// Delay startup data collection to ensure all routes are registered
		go func() {
			time.Sleep(time.Second)
			client.SetStartupData(getRoutes(r), getVersions(config.AppVersion), "go:gin")
		}()
	}

	return func(c *gin.Context) {
		if !client.IsEnabled() {
			c.Next()
			return
		}

		// Get route pattern
		routePattern := c.FullPath()

		// Determine request size
		requestSize := common.ParseContentLength(c.Request.Header.Get("Content-Length"))

		// Cache request body if needed
		var requestBody []byte
		var requestReader *common.RequestReader
		captureRequestBody := client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogRequestBody &&
			client.RequestLogger.IsSupportedContentType(c.Request.Header.Get("Content-Type"))

		if c.Request.Body != nil && requestSize <= common.MaxBodySize {
			if captureRequestBody {
				// Capture the body for logging
				var err error
				requestBody, err = io.ReadAll(c.Request.Body)
				if err == nil {
					c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
					if requestSize == -1 {
						requestSize = int64(len(requestBody))
					}
				}
			} else if requestSize == -1 {
				// Only measure request body size
				requestReader = &common.RequestReader{Reader: c.Request.Body}
				c.Request.Body = requestReader
			}
		}

		// Prepare response writer to capture body if needed
		var responseBody bytes.Buffer
		var originalWriter gin.ResponseWriter
		if client.Config.RequestLoggingConfig != nil &&
			client.Config.RequestLoggingConfig.Enabled &&
			client.Config.RequestLoggingConfig.LogResponseBody {
			originalWriter = c.Writer
			c.Writer = &responseWriter{
				ResponseWriter:         c.Writer,
				body:                   &responseBody,
				isSupportedContentType: client.RequestLogger.IsSupportedContentType,
			}
		}

		start := time.Now()

		defer func() {
			duration := time.Since(start)
			statusCode := c.Writer.Status()

			// Update request size from reader if needed
			if requestReader != nil && requestSize == -1 {
				requestSize = requestReader.Size()
			}

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
			responseSize := common.ParseContentLength(c.Writer.Header().Get("Content-Length"))
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
								common.TruncateValidationErrorMessage(fieldError.Error()),
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
					Consumer:  consumerIdentifier,
					Method:    c.Request.Method,
					Path:      routePattern,
					URL:       common.GetFullURL(c.Request),
					Headers:   common.TransformHeaders(c.Request.Header),
					Size:      requestSize,
					Body:      requestBody,
				}
				response := common.Response{
					StatusCode:   statusCode,
					ResponseTime: float64(duration.Milliseconds()) / 1000.0,
					Headers:      common.TransformHeaders(c.Writer.Header()),
					Size:         responseSize,
					Body:         responseBody.Bytes(),
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

// Alias for backwards compatibility
var ApitallyMiddleware = Middleware

func CaptureValidationError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		// Store validation errors in the context for middleware
		c.Set("ApitallyValidationErrors", validationErrors)
	}
}

func SetConsumerIdentifier(c *gin.Context, consumerIdentifier string) {
	c.Set("ApitallyConsumer", consumerIdentifier)
}

func SetConsumer(c *gin.Context, consumer common.Consumer) {
	c.Set("ApitallyConsumer", consumer)
}
