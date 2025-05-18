package apitally

import (
	"bytes"
	"context"
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

type responseWriter struct {
	http.ResponseWriter
	statusCode             int
	size                   int64
	body                   *bytes.Buffer
	shouldCaptureBody      *bool
	isSupportedContentType func(string) bool
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
	if *w.shouldCaptureBody {
		w.body.Write(b)
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

func ApitallyMiddleware(r chi.Router, config *ApitallyConfig) func(http.Handler) http.Handler {
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

			// Get route pattern
			routePattern := getRoutePattern(r)

			// Determine request size
			requestSize := parseContentLength(r.Header.Get("Content-Length"))

			// Cache request body if needed
			var requestBody []byte
			if r.Body != nil &&
				(requestSize == -1 ||
					(client.Config.RequestLoggingConfig != nil &&
						client.Config.RequestLoggingConfig.Enabled &&
						client.Config.RequestLoggingConfig.LogRequestBody &&
						client.RequestLogger.IsSupportedContentType(r.Header.Get("Content-Type")))) {
				var err error
				requestBody, err = io.ReadAll(r.Body)
				if err == nil {
					r.Body = io.NopCloser(bytes.NewBuffer(requestBody))
					if requestSize == -1 {
						requestSize = int64(len(requestBody))
					}
				}
			}

			// Prepare response writer to capture body if needed
			var responseBody bytes.Buffer
			rw := &responseWriter{
				ResponseWriter:         w,
				body:                   &responseBody,
				isSupportedContentType: client.RequestLogger.IsSupportedContentType,
			}

			start := time.Now()

			defer func() {
				duration := time.Since(start)
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
				if consumer := r.Context().Value(consumerKey); consumer != nil {
					if consumerObj := internal.ConsumerFromStringOrObject(consumer); consumerObj != nil {
						consumerIdentifier = consumerObj.Identifier
						client.ConsumerRegistry.AddOrUpdateConsumer(consumerObj)
					}
				}

				// Determine response size
				responseSize := parseContentLength(rw.Header().Get("Content-Length"))
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
							r.Method,
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
						Method:    r.Method,
						Path:      routePattern,
						URL:       getFullURL(r),
						Headers:   transformHeaders(r.Header),
						Size:      requestSize,
						Body:      requestBody,
					}
					response := common.Response{
						StatusCode:   statusCode,
						ResponseTime: float64(duration.Milliseconds()) / 1000.0,
						Headers:      transformHeaders(rw.Header()),
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

	validationErrors, ok := err.(validator.ValidationErrors)
	if ok {
		ctx := r.Context()
		*r = *r.WithContext(context.WithValue(ctx, validationErrorsKey, validationErrors))
	}
}
