package internal

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// exportSpan is Apitally's copy of a span. Embedding the original satisfies
// ReadOnlySpan's unexported method. The copy owns the storage it overrides and
// never modifies the original's slices, which the application's processors
// share.
type exportSpan struct {
	sdktrace.ReadOnlySpan
	attributes []attribute.KeyValue
	resource   *resource.Resource
	kind       trace.SpanKind
	endTime    time.Time
}

func newExportSpanCopy(span sdktrace.ReadOnlySpan, attrs []attribute.KeyValue) *exportSpan {
	return &exportSpan{ReadOnlySpan: span, attributes: attrs, resource: span.Resource(), kind: span.SpanKind(), endTime: span.EndTime()}
}

func (s *exportSpan) Attributes() []attribute.KeyValue { return s.attributes }
func (s *exportSpan) Resource() *resource.Resource     { return s.resource }
func (s *exportSpan) SpanKind() trace.SpanKind         { return s.kind }
func (s *exportSpan) EndTime() time.Time               { return s.endTime }

// newExportSpan copies an ended request member for export. A reused SERVER
// span's copy carries Apitally's attributes and ends no earlier than
// transport observation. Other SERVER spans in the request come from stacked
// HTTP instrumentation and are exported as INTERNAL.
func (r *sdkRuntime) newExportSpan(s *RequestState, span sdktrace.ReadOnlySpan) *exportSpan {
	c := newExportSpanCopy(span, span.Attributes())
	c.resource = r.exportResource(span.Resource())
	if span.SpanContext().SpanID() != s.span.SpanContext().SpanID() {
		if c.kind == trace.SpanKindServer {
			c.kind = trace.SpanKindInternal
			scope := span.InstrumentationScope().Name
			warnOnce("duplicate-server-span-"+scope, "Instrumentation scope "+scope+" starts SERVER spans inside Apitally's request span. Apitally exports them as INTERNAL spans, but other exporters receive two SERVER spans per request. Install that instrumentation outside Apitally's middleware.")
		}
		return c
	}
	if !s.isSpanCreated {
		c.attributes = mergeAttributes(span.Attributes(), s.requestAttributes, s.transportAttributes)
		if s.transportEnd.After(c.endTime) {
			c.endTime = s.transportEnd
		}
	}
	return c
}

// exportResource returns res with Apitally's process identity and
// environment, which spans of an application's provider lack.
func (r *sdkRuntime) exportResource(res *resource.Resource) *resource.Resource {
	if res == r.resource {
		return res
	}
	r.exportResourcesMu.Lock()
	defer r.exportResourcesMu.Unlock()
	if merged, ok := r.exportResources[res]; ok {
		return merged
	}
	merged, _ := resource.Merge(res, resource.NewSchemaless(
		attribute.String("service.instance.id", instanceID),
		attribute.String("deployment.environment.name", r.settings.config.Env),
	))
	r.exportResources[res] = merged
	return merged
}

// spanExporter redacts export copies on the span batch processor's goroutine
// and encodes them with the otlptrace exporter, whose client appends to the
// traces spool.
type spanExporter struct {
	redaction *redaction
	otlp      *otlptrace.Exporter
}

func newSpanExporter(red *redaction, sp *spool) *spanExporter {
	return &spanExporter{redaction: red, otlp: otlptrace.NewUnstarted(spoolTraceClient{spool: sp})}
}

func (e *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	defer recoverAndLogPanic("span export")
	processed := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if c, ok := e.process(span); ok {
			processed = append(processed, c)
		}
	}
	for len(processed) > 0 {
		n := min(len(processed), recordsPerEncodedChunk)
		_ = e.otlp.ExportSpans(ctx, processed[:n])
		processed = processed[n:]
	}
	return nil
}

func (e *spanExporter) Shutdown(context.Context) error { return nil }

// process drops a span whose redaction fails, so it never leaves the process
// unredacted.
func (e *spanExporter) process(span sdktrace.ReadOnlySpan) (_ sdktrace.ReadOnlySpan, ok bool) {
	defer func() {
		if p := recover(); p != nil {
			logPanic("span redaction", p)
			ok = false
		}
	}()
	c, isCopy := span.(*exportSpan)
	if !isCopy {
		return nil, false
	}
	c.attributes = e.redaction.redactSpanAttributes(c.attributes)
	return c, true
}

type spoolTraceClient struct {
	spool *spool
}

func (c spoolTraceClient) Start(context.Context) error { return nil }
func (c spoolTraceClient) Stop(context.Context) error  { return nil }

func (c spoolTraceClient) UploadTraces(_ context.Context, spans []*tracepb.ResourceSpans) error {
	c.spool.appendMessage(signalTraces, &tracepb.TracesData{ResourceSpans: spans})
	return nil
}
