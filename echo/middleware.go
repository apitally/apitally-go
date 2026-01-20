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
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
)

// Middleware returns the Apitally middleware for Echo.
//
// For more information, see:
//   - Setup guide: https://docs.apitally.io/frameworks/echo
//   - Reference: https://docs.apitally.io/reference/go
func Middleware(e *echo.Echo, config *Config) echo.MiddlewareFunc {
	client := internal.InitApitallyClient(*config)

	// Sync should only be disabled for testing purposes
	if !config.DisableSync {
		client.StartSync()

		// Delay startup data collection to ensure all routes are registered
		go func() {
			time.Sleep(time.Second)
			client.SetStartupData(getRoutes(e), getVersions(config.AppVersion), "go:echo")
		}()
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !client.IsEnabled() || c.Request().Method == "OPTIONS" {
				return next(c)
			}

			// Start span collection
			spanHandle := client.SpanCollector.StartSpan(c.Request().Context())
			traceID := spanHandle.TraceID()

			// Inject span context into request
			c.SetRequest(c.Request().WithContext(spanHandle.Context()))

			// Determine request size
			requestSize := common.ParseContentLength(c.Request().Header.Get("Content-Length"))

			// Cache request body if needed
			var requestBody []byte
			var requestReader *common.RequestReader
			captureRequestBody := client.Config.RequestLogging != nil &&
				client.Config.RequestLogging.Enabled &&
				client.Config.RequestLogging.LogRequestBody &&
				client.RequestLogger.IsSupportedContentType(c.Request().Header.Get("Content-Type"))

			if c.Request().Body != nil && requestSize <= common.MaxBodySize {
				if captureRequestBody {
					// Capture the body for logging
					var err error
					requestBody, err = io.ReadAll(c.Request().Body)
					if err == nil {
						c.Request().Body = io.NopCloser(bytes.NewBuffer(requestBody))
						requestSize = int64(len(requestBody))
					}
				} else if requestSize == -1 {
					// Only measure request body size
					requestReader = &common.RequestReader{Reader: c.Request().Body}
					c.Request().Body = requestReader
				}
			}

			// Prepare response writer to capture body if needed
			var responseBody bytes.Buffer
			rw := &common.ResponseWriter{
				ResponseWriter: c.Response().Writer,
				Body:           &responseBody,
				CaptureBody: client.Config.RequestLogging != nil &&
					client.Config.RequestLogging.Enabled &&
					client.Config.RequestLogging.LogResponseBody,
				IsSupportedContentType: client.RequestLogger.IsSupportedContentType,
			}
			c.Response().Writer = rw

			start := time.Now()

			defer func() {
				duration := time.Since(start)
				routePattern := c.Path()
				statusCode := rw.Status()

				// End span collection and get spans
				spanHandle.SetName(fmt.Sprintf("%s %s", c.Request().Method, routePattern))
				spans := spanHandle.End()

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
				if consumer := c.Get("ApitallyConsumer"); consumer != nil {
					if consumerObj := internal.ConsumerFromStringOrObject(consumer); consumerObj != nil {
						consumerIdentifier = consumerObj.Identifier
						client.ConsumerRegistry.AddOrUpdateConsumer(consumerObj)
					}
				}

				// Determine response size
				responseSize := common.ParseContentLength(c.Response().Header().Get("Content-Length"))
				if responseSize == -1 {
					responseSize = rw.Size()
				}

				// Count request
				if routePattern != "" {
					client.RequestCounter.AddRequest(
						consumerIdentifier,
						c.Request().Method,
						routePattern,
						statusCode,
						float64(duration.Milliseconds())/1000.0,
						requestSize,
						responseSize,
					)

					// Count validation errors if any
					if valErrValue := c.Get("ApitallyValidationErrors"); valErrValue != nil {
						validationErrors, ok := valErrValue.(validator.ValidationErrors)
						if ok {
							for _, fieldError := range validationErrors {
								client.ValidationErrorCounter.AddValidationError(
									consumerIdentifier,
									c.Request().Method,
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
							c.Request().Method,
							routePattern,
							recoveredErr,
							stackTrace,
						)
					}
				}

				// Log request if enabled
				if client.Config.RequestLogging != nil && client.Config.RequestLogging.Enabled {
					request := common.Request{
						Timestamp: float64(time.Now().UnixMilli()) / 1000.0,
						Consumer:  consumerIdentifier,
						Method:    c.Request().Method,
						Path:      routePattern,
						URL:       common.GetFullURL(c.Request()),
						Headers:   common.TransformHeaders(c.Request().Header),
						Size:      requestSize,
						Body:      requestBody,
					}
					response := common.Response{
						StatusCode:   statusCode,
						ResponseTime: float64(duration.Milliseconds()) / 1000.0,
						Headers:      common.TransformHeaders(c.Response().Header()),
						Size:         responseSize,
						Body:         responseBody.Bytes(),
					}
					client.RequestLogger.LogRequest(&request, &response, recoveredErr, stackTrace, spans, traceID)
				}

				// Re-panic if there was a panic
				if panicValue != nil {
					panic(panicValue)
				}
			}()

			return next(c)
		}
	}
}

func CaptureValidationError(c echo.Context, err error) {
	if err == nil {
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		c.Set("ApitallyValidationErrors", validationErrors)
	}
}

func SetConsumerIdentifier(c echo.Context, consumerIdentifier string) {
	c.Set("ApitallyConsumer", consumerIdentifier)
}

func SetConsumer(c echo.Context, consumer common.Consumer) {
	c.Set("ApitallyConsumer", consumer)
}
