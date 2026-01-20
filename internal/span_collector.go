package internal

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SpanData represents a collected span for serialization.
type SpanData struct {
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	StartTime    int64          `json:"start_time"`
	EndTime      int64          `json:"end_time"`
	Status       string         `json:"status,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// SpanHandle provides context management for a root span.
type SpanHandle struct {
	collector *SpanCollector
	span      trace.Span
	ctx       context.Context
	traceID   trace.TraceID
}

// Context returns the context with the span for injecting into the request.
// Returns the original context if disabled.
func (h *SpanHandle) Context() context.Context {
	return h.ctx
}

// TraceID returns the trace ID as a hex string (empty if disabled).
func (h *SpanHandle) TraceID() string {
	if !h.traceID.IsValid() {
		return ""
	}
	return h.traceID.String()
}

// SetName updates the root span name (no-op if disabled).
func (h *SpanHandle) SetName(name string) {
	if h.span != nil {
		h.span.SetName(name)
	}
}

// End ends the root span and returns collected spans (nil if disabled).
func (h *SpanHandle) End() []SpanData {
	if h.span == nil {
		return nil
	}
	h.span.End()
	return h.collector.getAndClearSpans(h.traceID)
}

// SpanCollector implements sdktrace.SpanProcessor to collect spans for request logging.
type SpanCollector struct {
	enabled         bool
	tracer          trace.Tracer
	includedSpanIDs map[trace.TraceID]map[trace.SpanID]struct{}
	collectedSpans  map[trace.TraceID][]SpanData
	mu              sync.RWMutex
}

// NewSpanCollector creates a new SpanCollector.
func NewSpanCollector(enabled bool) *SpanCollector {
	sc := &SpanCollector{
		enabled:         enabled,
		includedSpanIDs: make(map[trace.TraceID]map[trace.SpanID]struct{}),
		collectedSpans:  make(map[trace.TraceID][]SpanData),
	}

	if enabled {
		sc.setupTracerProvider()
	}

	return sc
}

// IsEnabled returns whether span collection is enabled.
func (sc *SpanCollector) IsEnabled() bool {
	return sc.enabled
}

// setupTracerProvider sets up the tracer provider, integrating with existing provider if available.
func (sc *SpanCollector) setupTracerProvider() {
	provider := otel.GetTracerProvider()

	// Check if it's an SDK TracerProvider with RegisterSpanProcessor
	if sdkProvider, ok := provider.(*sdktrace.TracerProvider); ok {
		sdkProvider.RegisterSpanProcessor(sc)
		sc.tracer = provider.Tracer("apitally")
		return
	}

	// Otherwise create our own provider
	newProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sc),
	)
	otel.SetTracerProvider(newProvider)
	sc.tracer = newProvider.Tracer("apitally")
}

// StartSpan creates a root span and returns a SpanHandle.
func (sc *SpanCollector) StartSpan(ctx context.Context) *SpanHandle {
	if !sc.enabled || sc.tracer == nil {
		return &SpanHandle{
			collector: sc,
			ctx:       ctx,
		}
	}

	ctx, span := sc.tracer.Start(ctx, "root")
	spanCtx := span.SpanContext()
	traceID := spanCtx.TraceID()

	sc.mu.Lock()
	sc.includedSpanIDs[traceID] = map[trace.SpanID]struct{}{
		spanCtx.SpanID(): {},
	}
	sc.collectedSpans[traceID] = []SpanData{}
	sc.mu.Unlock()

	return &SpanHandle{
		collector: sc,
		span:      span,
		ctx:       ctx,
		traceID:   traceID,
	}
}

// getAndClearSpans retrieves all collected spans for a trace and cleans up.
func (sc *SpanCollector) getAndClearSpans(traceID trace.TraceID) []SpanData {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	spans := sc.collectedSpans[traceID]
	delete(sc.collectedSpans, traceID)
	delete(sc.includedSpanIDs, traceID)
	return spans
}

// OnStart is called when a span starts. Implements sdktrace.SpanProcessor.
func (sc *SpanCollector) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	if !sc.enabled {
		return
	}

	spanCtx := s.SpanContext()
	traceID := spanCtx.TraceID()
	spanID := spanCtx.SpanID()

	sc.mu.Lock()
	defer sc.mu.Unlock()

	included, ok := sc.includedSpanIDs[traceID]
	if !ok {
		return
	}

	// Check if parent span is in our included set
	parentSpanCtx := s.Parent()
	if parentSpanCtx.IsValid() {
		if _, parentIncluded := included[parentSpanCtx.SpanID()]; parentIncluded {
			included[spanID] = struct{}{}
		}
	}
}

// OnEnd is called when a span ends. Implements sdktrace.SpanProcessor.
func (sc *SpanCollector) OnEnd(s sdktrace.ReadOnlySpan) {
	if !sc.enabled {
		return
	}

	spanCtx := s.SpanContext()
	traceID := spanCtx.TraceID()
	spanID := spanCtx.SpanID()

	sc.mu.Lock()
	defer sc.mu.Unlock()

	included, ok := sc.includedSpanIDs[traceID]
	if !ok {
		return
	}

	if _, isIncluded := included[spanID]; !isIncluded {
		return
	}

	data := sc.serializeSpan(s)
	sc.collectedSpans[traceID] = append(sc.collectedSpans[traceID], data)
}

// serializeSpan converts a ReadOnlySpan to SpanData.
func (sc *SpanCollector) serializeSpan(s sdktrace.ReadOnlySpan) SpanData {
	spanCtx := s.SpanContext()

	data := SpanData{
		SpanID:    spanCtx.SpanID().String(),
		Name:      s.Name(),
		Kind:      s.SpanKind().String(),
		StartTime: s.StartTime().UnixNano(),
		EndTime:   s.EndTime().UnixNano(),
	}

	// Set parent span ID if valid
	parentSpanCtx := s.Parent()
	if parentSpanCtx.IsValid() {
		data.ParentSpanID = parentSpanCtx.SpanID().String()
	}

	// Set status if not unset
	status := s.Status()
	if status.Code != 0 { // 0 is Unset
		data.Status = status.Code.String()
	}

	// Set attributes if any
	attrs := s.Attributes()
	if len(attrs) > 0 {
		data.Attributes = make(map[string]any, len(attrs))
		for _, kv := range attrs {
			data.Attributes[string(kv.Key)] = kv.Value.AsInterface()
		}
	}

	return data
}

// Shutdown is called when the SDK shuts down. Implements sdktrace.SpanProcessor.
func (sc *SpanCollector) Shutdown(ctx context.Context) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.enabled = false
	sc.includedSpanIDs = make(map[trace.TraceID]map[trace.SpanID]struct{})
	sc.collectedSpans = make(map[trace.TraceID][]SpanData)
	return nil
}

// ForceFlush is called to force flush any pending spans. Implements sdktrace.SpanProcessor.
func (sc *SpanCollector) ForceFlush(ctx context.Context) error {
	// Nothing to flush since we collect spans synchronously
	return nil
}
