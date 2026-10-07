package apitally

import (
	"context"

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

// SetConsumer identifies the API consumer making the request, for example
// in authentication middleware. ctx is the request context, or a context
// derived from it. The consumer's name, group and attributes update the
// consumer in Apitally. It does nothing outside a request monitored by
// Apitally.
func SetConsumer(ctx context.Context, consumer Consumer) {
	internal.SetConsumer(ctx, consumer)
}

// SetRequestAttributes sets attributes on the request's span, for example to
// use in Config.SampleOnResponse. ctx is the request context, or a context
// derived from it. It does nothing outside a request monitored by Apitally.
func SetRequestAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	internal.SetRequestAttributes(ctx, attrs...)
}

// CaptureError captures err as the request's error, with the caller's stack
// trace. Only the first error captured for a request is kept, and it counts
// as a server error when the response status is 500. ctx is the request
// context, or a context derived from it. It does nothing outside a request
// monitored by Apitally.
func CaptureError(ctx context.Context, err error) {
	internal.CaptureError(ctx, err)
}

// CaptureValidationError reports the validation errors in err, which are
// recognized when err is or wraps go-playground/validator's
// ValidationErrors. ctx is the request context, or a context derived from
// it. It does nothing outside a request monitored by Apitally.
func CaptureValidationError(ctx context.Context, err error) {
	internal.CaptureValidationError(ctx, err)
}
