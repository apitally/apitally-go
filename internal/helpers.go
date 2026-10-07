package internal

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
)

// The helpers do nothing outside a monitored request.

func SetConsumer(ctx context.Context, consumer root.Consumer) {
	defer recoverAndLogPanic("SetConsumer")
	if s := RequestStateFromContext(ctx); s != nil {
		s.mu.Lock()
		s.consumer = mergeConsumer(s.consumer, consumer)
		s.mu.Unlock()
	}
}

func SetRequestAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	if s := RequestStateFromContext(ctx); s != nil {
		s.span.SetAttributes(attrs...)
	}
}

// CaptureError captures err with the caller's stack trace.
func CaptureError(ctx context.Context, err error) {
	defer recoverAndLogPanic("CaptureError")
	if s := RequestStateFromContext(ctx); s != nil && err != nil {
		s.captureError(err, callerStack())
	}
}

func CaptureValidationError(ctx context.Context, err error) {
	defer recoverAndLogPanic("CaptureValidationError")
	if s := RequestStateFromContext(ctx); s != nil {
		details := validationDetails(err)
		s.mu.Lock()
		s.validationDetails = append(s.validationDetails, details...)
		s.mu.Unlock()
	}
}
