package internal

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

// SetRequestAttributes sets attributes on the SERVER span of the request in
// ctx. It does nothing outside a monitored request.
func SetRequestAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	if s := requestStateFromContext(ctx); s != nil {
		s.span.SetAttributes(attrs...)
	}
}
