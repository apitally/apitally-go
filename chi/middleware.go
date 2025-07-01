package apitally

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
)

type contextKey string

const (
	validationErrorsKey contextKey = "ApitallyValidationErrors"
	consumerKey         contextKey = "ApitallyConsumer"
)

// Middleware returns the Apitally middleware for Chi.
//
// For more information, see:
//   - Setup guide: https://docs.apitally.io/frameworks/chi
//   - Reference: https://docs.apitally.io/reference/go
func Middleware(r chi.Router, config *Config) func(http.Handler) http.Handler {
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
			client.SetStartupData(getRoutes(r), getVersions(config.AppVersion), "go:chi")
		}()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !client.IsEnabled() {
				next.ServeHTTP(w, r)
				return
			}

			// Determine request size
			requestSize := common.ParseContentLength(r.Header.Get("Content-Length"))

			// Cache request body if needed
			var requestBody []byte
			var requestReader *common.RequestReader
			captureRequestBody := client.Config.RequestLogging != nil &&
				client.Config.RequestLogging.Enabled &&
				client.Config.RequestLogging.LogRequestBody &&
				client.RequestLogger.IsSupportedContentType(r.Header.Get("Content-Type"))

			if r.Body != nil && requestSize <= common.MaxBodySize {
				if captureRequestBody {
					// Capture the body for logging
					var err error
					requestBody, err = io.ReadAll(r.Body)
					if err == nil {
						r.Body = io.NopCloser(bytes.NewBuffer(requestBody))
						requestSize = int64(len(requestBody))
					}
				} else if requestSize == -1 {
					// Only measure request body size
					requestReader = &common.RequestReader{Reader: r.Body}
					r.Body = requestReader
				}
			}

			// Prepare response writer to capture body if needed
			var responseBody bytes.Buffer
			rw := &common.ResponseWriter{
				ResponseWriter: w,
				Body:           &responseBody,
				CaptureBody: client.Config.RequestLogging != nil &&
					client.Config.RequestLogging.Enabled &&
					client.Config.RequestLogging.LogResponseBody,
				IsSupportedContentType: client.RequestLogger.IsSupportedContentType,
			}

			start := time.Now()

			defer func() {
				duration := time.Since(start)
				routePattern := getRoutePattern(r)
				statusCode := rw.Status()

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
				if consumer := r.Context().Value(consumerKey); consumer != nil {
					if consumerObj := internal.ConsumerFromStringOrObject(consumer); consumerObj != nil {
						consumerIdentifier = consumerObj.Identifier
						client.ConsumerRegistry.AddOrUpdateConsumer(consumerObj)
					}
				}

				// Determine response size
				responseSize := common.ParseContentLength(rw.Header().Get("Content-Length"))
				if responseSize == -1 {
					responseSize = rw.Size()
				}

				// Count request
				if routePattern != "" {
					client.RequestCounter.AddRequest(
						consumerIdentifier,
						r.Method,
						routePattern,
						statusCode,
						float64(duration.Milliseconds())/1000.0,
						requestSize,
						responseSize,
					)

					// Count validation errors if any
					if valErrValue := r.Context().Value(validationErrorsKey); valErrValue != nil {
						validationErrors, ok := valErrValue.(validator.ValidationErrors)
						if ok {
							for _, fieldError := range validationErrors {
								client.ValidationErrorCounter.AddValidationError(
									consumerIdentifier,
									r.Method,
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
							r.Method,
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
						Method:    r.Method,
						Path:      routePattern,
						URL:       common.GetFullURL(r),
						Headers:   common.TransformHeaders(r.Header),
						Size:      requestSize,
						Body:      requestBody,
					}
					response := common.Response{
						StatusCode:   statusCode,
						ResponseTime: float64(duration.Milliseconds()) / 1000.0,
						Headers:      common.TransformHeaders(rw.Header()),
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

			next.ServeHTTP(rw, r)
		})
	}
}

func CaptureValidationError(r *http.Request, err error) {
	if err == nil {
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		ctx := r.Context()
		*r = *r.WithContext(context.WithValue(ctx, validationErrorsKey, validationErrors))
	}
}

func SetConsumerIdentifier(r *http.Request, consumerIdentifier string) {
	ctx := r.Context()
	*r = *r.WithContext(context.WithValue(ctx, consumerKey, consumerIdentifier))
}

func SetConsumer(r *http.Request, consumer common.Consumer) {
	ctx := r.Context()
	*r = *r.WithContext(context.WithValue(ctx, consumerKey, consumer))
}
