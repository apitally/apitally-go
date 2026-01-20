package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSpanCollectorDisabled(t *testing.T) {
	collector := NewSpanCollector(false)
	assert.False(t, collector.IsEnabled())
	assert.Nil(t, collector.tracer)

	// StartSpan should return a valid no-op handle
	handle := collector.StartSpan(context.Background())
	assert.NotNil(t, handle)
	assert.Equal(t, "", handle.TraceID())

	// Context should be the original context
	ctx := context.WithValue(context.Background(), "test", "value")
	handle = collector.StartSpan(ctx)
	assert.Equal(t, ctx, handle.Context())

	// SetName should be a no-op
	handle.SetName("test")

	// End should return nil
	spans := handle.End()
	assert.Nil(t, spans)
}

func TestSpanCollectorEnabled(t *testing.T) {
	// Reset global tracer provider
	otel.SetTracerProvider(nil)

	collector := NewSpanCollector(true)
	assert.True(t, collector.IsEnabled())
	assert.NotNil(t, collector.tracer)

	// StartSpan should return a handle with valid trace ID
	handle := collector.StartSpan(context.Background())
	assert.NotNil(t, handle)
	assert.NotEmpty(t, handle.TraceID())
	assert.Len(t, handle.TraceID(), 32) // Trace ID is 32 hex characters

	// Context should contain the span
	ctx := handle.Context()
	span := trace.SpanFromContext(ctx)
	assert.True(t, span.SpanContext().IsValid())

	// End should return the root span
	spans := handle.End()
	assert.NotNil(t, spans)
	assert.Len(t, spans, 1)
	assert.Equal(t, "root", spans[0].Name)
	assert.Empty(t, spans[0].ParentSpanID) // Root span has no parent
}

func TestSpanCollectorWithChildSpans(t *testing.T) {
	// Reset global tracer provider
	otel.SetTracerProvider(nil)

	collector := NewSpanCollector(true)

	// Start root span
	handle := collector.StartSpan(context.Background())
	ctx := handle.Context()

	// Create child spans using the tracer
	tracer := otel.Tracer("test")
	_, childSpan1 := tracer.Start(ctx, "child1")
	childSpan1.End()

	_, childSpan2 := tracer.Start(ctx, "child2")
	childSpan2.End()

	// Set root span name
	handle.SetName("GET /users")

	// End and collect spans
	spans := handle.End()
	assert.NotNil(t, spans)
	assert.Len(t, spans, 3) // root + 2 children

	// Find spans by name
	var rootSpan, child1, child2 *SpanData
	for i := range spans {
		switch spans[i].Name {
		case "GET /users":
			rootSpan = &spans[i]
		case "child1":
			child1 = &spans[i]
		case "child2":
			child2 = &spans[i]
		}
	}

	assert.NotNil(t, rootSpan)
	assert.NotNil(t, child1)
	assert.NotNil(t, child2)

	// Root span should have no parent
	assert.Empty(t, rootSpan.ParentSpanID)

	// Child spans should have root as parent
	assert.Equal(t, rootSpan.SpanID, child1.ParentSpanID)
	assert.Equal(t, rootSpan.SpanID, child2.ParentSpanID)
}

func TestSpanCollectorDoesNotCollectUnrelatedSpans(t *testing.T) {
	// Reset global tracer provider
	otel.SetTracerProvider(nil)

	collector := NewSpanCollector(true)

	// Create a span outside of our collection
	tracer := otel.Tracer("test")
	_, outsideSpan := tracer.Start(context.Background(), "outside_span")
	outsideSpan.End()

	// Start our collection
	handle := collector.StartSpan(context.Background())
	ctx := handle.Context()

	// Create a child span inside our collection
	_, childSpan := tracer.Start(ctx, "inside_span")
	childSpan.End()

	// End and collect spans
	spans := handle.End()
	assert.NotNil(t, spans)

	// Should only have root and inside_span, not outside_span
	spanNames := make([]string, len(spans))
	for i, s := range spans {
		spanNames[i] = s.Name
	}
	assert.Contains(t, spanNames, "root")
	assert.Contains(t, spanNames, "inside_span")
	assert.NotContains(t, spanNames, "outside_span")
}

func TestSpanDataSerialization(t *testing.T) {
	// Reset global tracer provider
	otel.SetTracerProvider(nil)

	collector := NewSpanCollector(true)
	handle := collector.StartSpan(context.Background())

	// Create a span with attributes
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(handle.Context(), "test_span")
	span.SetAttributes(
	// Using string attribute for simplicity
	)
	span.End()
	_ = ctx

	spans := handle.End()
	assert.NotNil(t, spans)

	// Find the test span
	var testSpan *SpanData
	for i := range spans {
		if spans[i].Name == "test_span" {
			testSpan = &spans[i]
			break
		}
	}

	assert.NotNil(t, testSpan)
	assert.Len(t, testSpan.SpanID, 16) // Span ID is 16 hex characters
	assert.Greater(t, testSpan.StartTime, int64(0))
	assert.Greater(t, testSpan.EndTime, int64(0))
	assert.GreaterOrEqual(t, testSpan.EndTime, testSpan.StartTime)
}

func TestSpanCollectorShutdown(t *testing.T) {
	// Reset global tracer provider
	otel.SetTracerProvider(nil)

	collector := NewSpanCollector(true)
	assert.True(t, collector.IsEnabled())

	// Start a span
	handle := collector.StartSpan(context.Background())
	_ = handle.TraceID()

	// Shutdown
	err := collector.Shutdown(context.Background())
	assert.NoError(t, err)
	assert.False(t, collector.IsEnabled())

	// Maps should be cleared
	assert.Empty(t, collector.includedSpanIDs)
	assert.Empty(t, collector.collectedSpans)
}
