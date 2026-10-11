package apitally

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal"
)

// Config configures Apitally. Create it with [NewConfig].
type Config = root.Config

// Consumer identifies the API consumer making a request.
type Consumer = root.Consumer

// LogRecord is a captured application log record passed to
// Config.MaskLogRecord.
type LogRecord = root.LogRecord

// NewConfig returns a Config with the default settings.
func NewConfig() *Config {
	return root.NewConfig()
}

// Shutdown exports the remaining telemetry and stops Apitally. Call it when
// the application shuts down, after the HTTP server has stopped. It returns
// the context's error if the context ends before all telemetry is delivered.
// Without it, telemetry from up to the last export interval is lost at exit.
func Shutdown(ctx context.Context) error {
	return internal.Shutdown(ctx)
}

// NewSlogHandler returns a slog.Handler that captures the records logged with
// a request's context for Apitally and passes every record to next
// unchanged, for example:
//
//	handler := slog.NewJSONHandler(os.Stdout, nil)
//	slog.SetDefault(slog.New(apitally.NewSlogHandler(handler)))
//
// Pass the request context when logging, such as with slog.InfoContext, to
// link records to the request. next must not be the handler of the default
// logger created by the slog package itself.
func NewSlogHandler(next slog.Handler) slog.Handler {
	return internal.NewSlogHandler(next)
}

// SetConsumer identifies the API consumer making the request, for example
// in authentication middleware. ctx is the request context, or a context
// derived from it. The consumer's name, group and attributes update the
// consumer in Apitally.
func SetConsumer(ctx context.Context, consumer Consumer) {
	internal.SetConsumer(ctx, consumer)
}

// SetRequestAttributes sets attributes on the request's span, for example to
// use in Config.SampleOnResponse. ctx is the request context, or a context
// derived from it.
func SetRequestAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	internal.SetRequestAttributes(ctx, attrs...)
}

// CaptureError captures err as the request's error, with the caller's stack
// trace. Only the first error captured for a request is kept. ctx is the
// request context, or a context derived from it.
func CaptureError(ctx context.Context, err error) {
	internal.CaptureError(ctx, err)
}
