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

type responseWriter struct {
	http.ResponseWriter
	statusCode             int
	size                   int64
	body                   *bytes.Buffer
	shouldCaptureBody      *bool
	isSupportedContentType func(string) bool
	exceededMaxSize        bool
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.shouldCaptureBody == nil {
		w.shouldCaptureBody = new(bool)
		*w.shouldCaptureBody = w.isSupportedContentType(w.Header().Get("Content-Type"))
	}
	if *w.shouldCaptureBody && !w.exceededMaxSize {
		if w.body.Len()+len(b) <= internal.MaxBodySize {
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

func (w *responseWriter) Status() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *responseWriter) Size() int64 {
	return w.size
}

func Middleware(e *echo.Echo, config *Config) echo.MiddlewareFunc {
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
			client.SetStartupData(getRoutes(e), getVersions(config.AppVersion), "go:echo")
		}()
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !client.IsEnabled() {
				return next(c)
			}

			// Determine request size
			requestSize := common.ParseContentLength(c.Request().Header.Get("Content-Length"))

			// Cache request body if needed
			var requestBody []byte
			if c.Request().Body != nil && requestSize <= internal.MaxBodySize &&
				(requestSize == -1 ||
					(client.Config.RequestLoggingConfig != nil &&
						client.Config.RequestLoggingConfig.Enabled &&
						client.Config.RequestLoggingConfig.LogRequestBody &&
						client.RequestLogger.IsSupportedContentType(c.Request().Header.Get("Content-Type")))) {
				var err error
				requestBody, err = io.ReadAll(c.Request().Body)
				if err == nil {
					c.Request().Body = io.NopCloser(bytes.NewBuffer(requestBody))
					if requestSize == -1 {
						requestSize = int64(len(requestBody))
					}
				}
			}

			// Prepare response writer to capture body if needed
			var responseBody bytes.Buffer
			rw := &responseWriter{
				ResponseWriter:         c.Response().Writer,
				body:                   &responseBody,
				isSupportedContentType: client.RequestLogger.IsSupportedContentType,
			}
			c.Response().Writer = rw

			start := time.Now()

			defer func() {
				duration := time.Since(start)
				routePattern := getRoutePattern(c)
				statusCode := rw.Status()

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
				if client.Config.RequestLoggingConfig != nil && client.Config.RequestLoggingConfig.Enabled {
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
					client.RequestLogger.LogRequest(&request, &response, recoveredErr, stackTrace)
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
