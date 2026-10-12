package apitally

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal"
)

// Config configures Apitally. Create it with [NewConfig], which sets the
// defaults.
type Config = root.Config

// Consumer identifies the API consumer making a request.
type Consumer = root.Consumer

// LogRecord is a log record captured by the handler from NewSlogHandler, as
// passed to Config.MaskLogRecord.
type LogRecord = root.LogRecord

// NewConfig returns a Config with the default settings.
func NewConfig() *Config {
	return root.NewConfig()
}

// Shutdown sends the remaining telemetry and stops Apitally. Call it after
// the HTTP server has stopped.
func Shutdown(ctx context.Context) error {
	return internal.Shutdown(ctx)
}

// NewSlogHandler returns a slog.Handler that captures log records for
// Apitally and passes every record to next unchanged:
//
//	handler := slog.NewJSONHandler(os.Stdout, nil)
//	slog.SetDefault(slog.New(apitally.NewSlogHandler(handler)))
//
// Only records logged with the request context, such as with
// slog.InfoContext, are captured, and only for requests included in request
// logs.
//
// Do not wrap slog.Default().Handler() before slog.SetDefault has been
// called. Setting the result as the default logger then causes a deadlock.
func NewSlogHandler(next slog.Handler) slog.Handler {
	return internal.NewSlogHandler(next)
}

// SetConsumer sets the API consumer making the request, for example in
// authentication middleware. ctx is the request context or a context derived
// from it.
func SetConsumer(ctx context.Context, consumer Consumer) {
	internal.SetConsumer(ctx, consumer)
}

// SetRequestAttributes sets attributes on the request's span. They are sent
// with the request's trace and are available to the SampleOnResponse,
// MaskRequestBody and MaskResponseBody callbacks. ctx is the request context
// or a context derived from it.
func SetRequestAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	internal.SetRequestAttributes(ctx, attrs...)
}

// CaptureError captures err as the request's error, with the caller's stack
// trace. Use it for errors the application handles itself; panics and errors
// returned to the framework are captured automatically. ctx is the request
// context or a context derived from it.
func CaptureError(ctx context.Context, err error) {
	internal.CaptureError(ctx, err)
}
