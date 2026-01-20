package apitally

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"slices"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

// Middleware returns the Apitally middleware for Fiber.
//
// For more information, see:
//   - Setup guide: https://docs.apitally.io/frameworks/fiber
//   - Reference: https://docs.apitally.io/reference/go
func Middleware(app *fiber.App, config *Config) fiber.Handler {
	client := internal.InitApitallyClient(*config)

	// Sync should only be disabled for testing purposes
	if !config.DisableSync {
		client.StartSync()

		app.Hooks().OnListen(func(data fiber.ListenData) error {
			client.SetStartupData(getRoutes(app), getVersions(config.AppVersion), "go:fiber")
			return nil
		})
	}

	return func(c *fiber.Ctx) error {
		if !client.IsEnabled() {
			return c.Next()
		}

		// Start span collection
		spanHandle := client.SpanCollector.StartSpan(c.UserContext())
		traceID := spanHandle.TraceID()

		// Inject span context into request
		c.SetUserContext(spanHandle.Context())

		// Determine request size
		requestSize := common.ParseContentLength(c.Get("Content-Length"))

		// Cache request body if needed
		var requestBody []byte
		if requestSize <= common.MaxBodySize &&
			(requestSize == -1 ||
				(client.Config.RequestLogging != nil &&
					client.Config.RequestLogging.Enabled &&
					client.Config.RequestLogging.LogRequestBody &&
					client.RequestLogger.IsSupportedContentType(c.Get("Content-Type")))) {
			requestBody = slices.Clone(c.Request().Body())
			if requestSize == -1 {
				requestSize = int64(len(requestBody))
			}
		}

		start := time.Now()

		defer func() {
			duration := time.Since(start)
			statusCode := int(c.Response().StatusCode())
			method := string(c.Route().Method)
			path := string(c.Route().Path)

			if method == "OPTIONS" {
				return
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

			// End span collection and get spans
			spanHandle.SetName(fmt.Sprintf("%s %s", method, path))
			spans := spanHandle.End()

			// Get consumer info if available
			var consumerIdentifier string
			if consumer := c.Locals("ApitallyConsumer"); consumer != nil {
				if consumerObj := internal.ConsumerFromStringOrObject(consumer); consumerObj != nil {
					consumerIdentifier = consumerObj.Identifier
					client.ConsumerRegistry.AddOrUpdateConsumer(consumerObj)
				}
			}

			// Determine response size
			responseSize := common.ParseContentLength(c.GetRespHeader("Content-Length"))

			// Cache response body if needed
			var responseBody []byte
			if responseSize == -1 ||
				(client.Config.RequestLogging != nil &&
					client.Config.RequestLogging.Enabled &&
					client.Config.RequestLogging.LogResponseBody) {
				responseBody = slices.Clone(c.Response().Body())
				responseSize = int64(len(responseBody))
			}

			// Count request
			if path != "" {
				client.RequestCounter.AddRequest(
					consumerIdentifier,
					method,
					path,
					statusCode,
					float64(duration.Milliseconds())/1000.0,
					requestSize,
					responseSize,
				)

				// Count validation errors if any
				if valErrValue := c.Locals("ApitallyValidationErrors"); valErrValue != nil {
					validationErrors, ok := valErrValue.(validator.ValidationErrors)
					if ok {
						for _, fieldError := range validationErrors {
							client.ValidationErrorCounter.AddValidationError(
								consumerIdentifier,
								method,
								path,
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
						method,
						path,
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
					Method:    method,
					Path:      path,
					URL:       getFullURL(c),
					Headers:   transformHeaders(c.GetReqHeaders()),
					Size:      requestSize,
					Body:      requestBody,
				}
				response := common.Response{
					StatusCode:   statusCode,
					ResponseTime: float64(duration.Milliseconds()) / 1000.0,
					Headers:      transformHeaders(c.GetRespHeaders()),
					Size:         responseSize,
					Body:         responseBody,
				}
				client.RequestLogger.LogRequest(&request, &response, recoveredErr, stackTrace, spans, traceID)
			}

			// Re-panic if there was a panic
			if panicValue != nil {
				panic(panicValue)
			}
		}()

		return c.Next()
	}
}

// Alias for backwards compatibility
var ApitallyMiddleware = Middleware

func CaptureValidationError(c *fiber.Ctx, err error) {
	if err == nil {
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		c.Locals("ApitallyValidationErrors", validationErrors)
	}
}

func SetConsumerIdentifier(c *fiber.Ctx, consumerIdentifier string) {
	c.Locals("ApitallyConsumer", consumerIdentifier)
}

func SetConsumer(c *fiber.Ctx, consumer common.Consumer) {
	c.Locals("ApitallyConsumer", consumer)
}
