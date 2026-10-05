package exportcopy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// The embedded interface supplies private() and future ReadOnlySpan methods.
type exportCopy struct {
	sdktrace.ReadOnlySpan
	attributes []attribute.KeyValue
	resource   *resource.Resource
	kind       trace.SpanKind
	body       []byte
}

var _ sdktrace.ReadOnlySpan = (*exportCopy)(nil)
var _ sdktrace.ReadOnlySpan = sdktrace.ReadWriteSpan(nil)

func (s *exportCopy) Attributes() []attribute.KeyValue { return s.attributes }
func (s *exportCopy) Resource() *resource.Resource     { return s.resource }
func (s *exportCopy) SpanKind() trace.SpanKind         { return s.kind }

func TestExportCopy(t *testing.T) {
	for _, ownership := range []string{"apitally", "user"} {
		t.Run(ownership, func(t *testing.T) {
			exporter := &copyExporter{}
			wrapper := &copyProcessor{next: newBatch(exporter)}
			userExporter := tracetest.NewInMemoryExporter()
			originalResource := resource.NewWithAttributes("https://example.com/resource/v1",
				attribute.String("service.instance.id", "user-instance"),
				attribute.String("deployment.environment.name", "user-env"),
				attribute.String("service.name", "checkout"),
				attribute.String("custom.resource", "preserved"))
			limits := sdktrace.NewSpanLimits()
			limits.AttributeCountLimit = 2
			limits.EventCountLimit = 2
			limits.LinkCountLimit = 1
			limits.AttributePerEventCountLimit = 1
			limits.AttributePerLinkCountLimit = 1
			opts := []sdktrace.TracerProviderOption{
				sdktrace.WithResource(originalResource),
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithRawSpanLimits(limits),
			}
			if ownership == "apitally" {
				opts = append(opts, sdktrace.WithSpanProcessor(wrapper))
			} else {
				opts = append(opts, sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(userExporter)))
			}
			provider := sdktrace.NewTracerProvider(opts...)
			cleanupProcessor(t, provider)
			if ownership == "user" {
				provider.RegisterSpanProcessor(wrapper)
			}

			tracer := provider.Tracer("poc.instrumentation", trace.WithInstrumentationVersion("1.2.3"),
				trace.WithSchemaURL("https://example.com/scope/v1"),
				trace.WithInstrumentationAttributes(attribute.String("scope.attr", "preserved")))
			parent := spanContext(true).WithRemote(true)
			start := time.Unix(1700000000, 0)
			ctx, server := tracer.Start(trace.ContextWithRemoteSpanContext(context.Background(), parent),
				"GET /orders", trace.WithSpanKind(trace.SpanKindServer), trace.WithTimestamp(start),
				trace.WithAttributes(attribute.String("url.query", "token=secret"), attribute.String("original", "kept")),
				trace.WithLinks(
					trace.Link{SpanContext: parent, Attributes: []attribute.KeyValue{attribute.String("link.a", "a"), attribute.String("link.b", "b")}},
					trace.Link{SpanContext: parent, Attributes: []attribute.KeyValue{attribute.String("link.a", "c"), attribute.String("link.b", "d")}}))
			server.SetAttributes(attribute.String("over-limit", "dropped"))
			server.AddEvent("evicted", trace.WithTimestamp(start.Add(time.Second)))
			server.AddEvent("kept-event", trace.WithTimestamp(start.Add(2*time.Second)),
				trace.WithAttributes(attribute.String("event.a", "a"), attribute.String("event.b", "b")))
			server.RecordError(errors.New("boom"), trace.WithTimestamp(start.Add(3*time.Second)))
			server.SetStatus(codes.Error, "request failed")
			_, duplicate := tracer.Start(ctx, "duplicate SERVER", trace.WithSpanKind(trace.SpanKindServer))
			duplicate.End()
			server.End(trace.WithTimestamp(start.Add(4 * time.Second)))
			flush(t, provider)

			copies := exporter.snapshot()
			equal(t, "exported copy count", len(copies), 2)
			for _, copied := range copies {
				original := copied.ReadOnlySpan
				equal(t, "stash", string(copied.body), "raw secret body")
				attrs := attributeMap(copied.Attributes())
				equal(t, "redacted query", attrs["url.query"], attribute.StringValue("token=[REDACTED]"))
				equal(t, "captured header", attrs["http.request.header.authorization"], attribute.StringSliceValue([]string{"[REDACTED]"}))
				for _, attr := range copied.Attributes() {
					if attr.Value.Emit() == string(copied.body) {
						t.Fatal("raw body appeared as an attribute")
					}
				}
				res := attributeMap(copied.Resource().Attributes())
				equal(t, "instance override", res["service.instance.id"], attribute.StringValue("apitally-instance"))
				equal(t, "environment override", res["deployment.environment.name"], attribute.StringValue("production"))
				equal(t, "preserved service", res["service.name"], attribute.StringValue("checkout"))
				equal(t, "preserved resource attr", res["custom.resource"], attribute.StringValue("preserved"))
				equal(t, "resource schema", copied.Resource().SchemaURL(), originalResource.SchemaURL())
				if original.Name() == "duplicate SERVER" {
					equal(t, "duplicate export kind", copied.SpanKind(), trace.SpanKindInternal)
					equal(t, "duplicate original kind", original.SpanKind(), trace.SpanKindServer)
				} else {
					equal(t, "root export kind", copied.SpanKind(), trace.SpanKindServer)
					equal(t, "original query", attributeMap(original.Attributes())["url.query"], attribute.StringValue("token=secret"))
					equal(t, "preserved span attr", attrs["original"], attribute.StringValue("kept"))
					equal(t, "root parent", copied.Parent(), parent)
					equal(t, "start time", copied.StartTime(), start)
					equal(t, "end time", copied.EndTime(), start.Add(4*time.Second))
					equal(t, "dropped attributes", copied.DroppedAttributes(), 1)
					equal(t, "dropped events", copied.DroppedEvents(), 1)
					equal(t, "dropped links", copied.DroppedLinks(), 1)
					equal(t, "children", copied.ChildSpanCount(), 1)
					equal(t, "exception event", copied.Events()[1].Name, "exception")
					equal(t, "event attribute drops", copied.Events()[0].DroppedAttributeCount, 1)
					equal(t, "link attribute drops", copied.Links()[0].DroppedAttributeCount, 1)
				}
				originalRes := attributeMap(original.Resource().Attributes())
				equal(t, "original instance", originalRes["service.instance.id"], attribute.StringValue("user-instance"))
				equal(t, "original environment", originalRes["deployment.environment.name"], attribute.StringValue("user-env"))
				for key, value := range originalRes {
					if key != "service.instance.id" && key != "deployment.environment.name" {
						equal(t, "preserved resource key "+string(key), res[key], value)
					}
				}
				equal(t, "original attribute count", len(original.Attributes()) <= 2, true)
				_, captured := attributeMap(original.Attributes())["http.request.header.authorization"]
				equal(t, "no original captured headers", captured, false)

				// SpanStub enumerates every exported ReadOnlySpan field. Normalize only overrides.
				got := tracetest.SpanStubFromReadOnlySpan(copied)
				want := tracetest.SpanStubFromReadOnlySpan(original)
				got.Attributes, got.Resource, got.SpanKind = want.Attributes, want.Resource, want.SpanKind
				equal(t, "all passthrough data", got, want)
				if ownership == "user" {
					found := false
					for _, userSpan := range userExporter.GetSpans() {
						if userSpan.SpanContext.SpanID() == original.SpanContext().SpanID() {
							equal(t, "user export unchanged", userSpan, want)
							found = true
						}
					}
					equal(t, "user saw original", found, true)
				}
			}
			if ownership == "user" {
				equal(t, "user span count", len(userExporter.GetSpans()), 2)
			}
		})
	}
}

func TestOnStartReadOnlySpan(t *testing.T) {
	called := false
	processor := &startProcessor{callback: func(span sdktrace.ReadOnlySpan) {
		called = true
		equal(t, "start attributes", attributeMap(span.Attributes())["before.return"], attribute.StringValue("visible"))
		equal(t, "processor attributes", attributeMap(span.Attributes())["processor.attr"], attribute.StringValue("visible"))
		equal(t, "live end time", span.EndTime().IsZero(), true)
		equal(t, "live SERVER kind", span.SpanKind(), trace.SpanKindServer)
	}}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(processor))
	cleanupProcessor(t, provider)
	_, span := provider.Tracer("poc").Start(context.Background(), "request", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("before.return", "visible")))
	equal(t, "callback before Start returns", called, true)
	span.SetAttributes(attribute.String("after.return", "later"))
	span.End()
}

func TestBatchSampledContext(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		exporter := &copyExporter{}
		bsp := newBatch(exporter)
		cleanupProcessor(t, bsp)
		original := tracetest.SpanStub{SpanContext: spanContext(sampled)}.Snapshot()
		copied := &exportCopy{ReadOnlySpan: original}
		bsp.OnEnd(copied)
		flush(t, bsp)
		equal(t, "embedded sampling honored", len(exporter.snapshot()), boolCount(sampled))
	}
	// A context override isolates which object's context BSP consults.
	for _, sampled := range []bool{false, true} {
		exporter := tracetest.NewInMemoryExporter()
		bsp := newBatch(exporter)
		cleanupProcessor(t, bsp)
		original := tracetest.SpanStub{SpanContext: spanContext(!sampled)}.Snapshot()
		copied := &contextCopy{exportCopy: &exportCopy{ReadOnlySpan: original}, context: spanContext(sampled)}
		bsp.OnEnd(copied)
		flush(t, bsp)
		equal(t, "overridden sampling honored", len(exporter.GetSpans()), boolCount(sampled))
	}
}

func TestBatchExplicitOptionsOverrideEnvironment(t *testing.T) {
	t.Setenv("OTEL_BSP_MAX_QUEUE_SIZE", "1")
	t.Setenv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "1")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "60000")
	t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", "1")
	exporter := &batchExporter{calls: make(chan batchCall, 4)}
	bsp := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithMaxQueueSize(8), sdktrace.WithMaxExportBatchSize(2),
		sdktrace.WithBatchTimeout(100*time.Millisecond), sdktrace.WithExportTimeout(3*time.Second))
	cleanupProcessor(t, bsp)
	// Read the SDK's exported logging representation, not private fields.
	data, err := json.Marshal(bsp.(interface{ MarshalLog() any }).MarshalLog())
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic struct {
		Config sdktrace.BatchSpanProcessorOptions
	}
	if err := json.Unmarshal(data, &diagnostic); err != nil {
		t.Fatal(err)
	}
	equal(t, "explicit BSP settings", diagnostic.Config, sdktrace.BatchSpanProcessorOptions{
		MaxQueueSize: 8, MaxExportBatchSize: 2, BatchTimeout: 100 * time.Millisecond, ExportTimeout: 3 * time.Second,
	})
	span := &exportCopy{ReadOnlySpan: tracetest.SpanStub{SpanContext: spanContext(true)}.Snapshot()}
	bsp.OnEnd(span)
	bsp.OnEnd(span)
	call := receiveBatch(t, exporter.calls)
	equal(t, "explicit batch size", call.size, 2)
	if call.budget < 2*time.Second || call.budget > 3*time.Second {
		t.Fatalf("export timeout budget = %s, want about 3s, not env's 1ms", call.budget)
	}
	bsp.OnEnd(span)
	equal(t, "explicit schedule exports without flush", receiveBatch(t, exporter.calls).size, 1)
}

type copyProcessor struct{ next sdktrace.SpanProcessor }

func (*copyProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *copyProcessor) OnEnd(original sdktrace.ReadOnlySpan) {
	attrs := make(map[attribute.Key]attribute.Value)
	for _, attr := range original.Attributes() {
		attrs[attr.Key] = attr.Value
	}
	attrs["url.query"] = attribute.StringValue("token=[REDACTED]")
	attrs["http.request.header.authorization"] = attribute.StringSliceValue([]string{"[REDACTED]"})
	values := make([]attribute.KeyValue, 0, len(attrs))
	for key, value := range attrs {
		values = append(values, attribute.KeyValue{Key: key, Value: value})
	}
	res, err := resource.Merge(original.Resource(), resource.NewSchemaless(
		attribute.String("service.instance.id", "apitally-instance"),
		attribute.String("deployment.environment.name", "production")))
	if err != nil {
		panic(err)
	}
	kind := original.SpanKind()
	if kind == trace.SpanKindServer && original.Parent().IsValid() && !original.Parent().IsRemote() {
		kind = trace.SpanKindInternal
	}
	p.next.OnEnd(&exportCopy{ReadOnlySpan: original, attributes: values, resource: res, kind: kind, body: []byte("raw secret body")})
}
func (p *copyProcessor) ForceFlush(ctx context.Context) error { return p.next.ForceFlush(ctx) }
func (p *copyProcessor) Shutdown(ctx context.Context) error   { return p.next.Shutdown(ctx) }

type copyExporter struct {
	mu    sync.Mutex
	spans []*exportCopy
}

func (e *copyExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, span := range spans {
		copied, ok := span.(*exportCopy)
		if !ok {
			return errors.New("BSP changed the concrete export-copy type")
		}
		if copied.body != nil && string(copied.body) != "raw secret body" {
			return errors.New("exporter could not read the private body stash")
		}
		// Exercise embedded snapshot reads on the BSP export goroutine.
		_ = tracetest.SpanStubFromReadOnlySpan(copied)
		e.spans = append(e.spans, copied)
	}
	return nil
}
func (*copyExporter) Shutdown(context.Context) error { return nil }
func (e *copyExporter) snapshot() []*exportCopy {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*exportCopy(nil), e.spans...)
}

type startProcessor struct{ callback func(sdktrace.ReadOnlySpan) }

func (p *startProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	span.SetAttributes(attribute.String("processor.attr", "visible"))
	p.callback(span)
}
func (*startProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*startProcessor) ForceFlush(context.Context) error { return nil }
func (*startProcessor) Shutdown(context.Context) error   { return nil }

type contextCopy struct {
	*exportCopy
	context trace.SpanContext
}

func (s *contextCopy) SpanContext() trace.SpanContext { return s.context }

type batchCall struct {
	size   int
	budget time.Duration
}

type batchExporter struct{ calls chan batchCall }

func (e *batchExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	deadline, _ := ctx.Deadline()
	e.calls <- batchCall{size: len(spans), budget: time.Until(deadline)}
	return nil
}
func (*batchExporter) Shutdown(context.Context) error { return nil }

func newBatch(exporter sdktrace.SpanExporter) sdktrace.SpanProcessor {
	return sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithMaxQueueSize(16), sdktrace.WithMaxExportBatchSize(8),
		sdktrace.WithBatchTimeout(time.Hour), sdktrace.WithExportTimeout(5*time.Second))
}

func cleanupProcessor(t *testing.T, p interface{ Shutdown(context.Context) error }) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
}

func flush(t *testing.T, p interface{ ForceFlush(context.Context) error }) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
}

func receiveBatch(t *testing.T, calls <-chan batchCall) batchCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("BSP did not export within 5s")
		return batchCall{}
	}
}

func spanContext(sampled bool) trace.SpanContext {
	flags := trace.TraceFlags(0)
	if sampled {
		flags = trace.FlagsSampled
	}
	state, _ := trace.ParseTraceState("poc=preserved")
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: flags, TraceState: state,
	})
}

func attributeMap(attrs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	values := make(map[attribute.Key]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	return values
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func equal(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", label, got, want)
	}
}
