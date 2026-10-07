package rootpoc

import (
	"context"
	"encoding/binary"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Handle struct {
	method string
	span   trace.Span
	req    *request
}

func (a *Apitally) Begin(ctx context.Context, info RequestInfo, headers propagation.TextMapCarrier) (context.Context, *Handle) {
	a.activate()
	h := &Handle{method: info.Method}
	if a.excluded(info.Path) {
		return ctx, h
	}
	attrs := info.attributes()
	root := trace.SpanFromContext(ctx)
	if !a.reusable(root) {
		if !trace.SpanContextFromContext(ctx).IsValid() {
			ctx = propagation.TraceContext{}.Extract(ctx, headers)
		}
		ctx, root = a.tracer.Start(ctx, info.Method, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
		h.span = root
	}
	live, ok := root.(sdktrace.ReadOnlySpan)
	if !ok || !root.IsRecording() || !a.sampleRequest(live, attrs) {
		return ctx, h
	}
	h.req = &request{a: a, root: root.SpanContext().SpanID(), reqAttrs: attrs}
	a.requests.register(h.req)
	return ctx, h
}

func (h *Handle) Finish(route string, attrs []attribute.KeyValue) {
	if h.span != nil {
		if route != "" {
			h.span.SetName(h.method + " " + route)
		}
		h.span.SetAttributes(attrs...)
	}
	if h.req != nil {
		h.req.observe(attrs)
	}
	if h.span != nil {
		h.span.End()
	}
}

func (a *Apitally) reusable(s trace.Span) bool {
	ro, ok := s.(sdktrace.ReadOnlySpan)
	return ok && s.IsRecording() && ro.SpanKind() == trace.SpanKindServer && s.TracerProvider() == trace.TracerProvider(a.provider) &&
		!a.requests.known(s.SpanContext().SpanID())
}

func (a *Apitally) excluded(path string) bool {
	for _, re := range a.cfg.ExcludePaths {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func (a *Apitally) sampleRequest(live sdktrace.ReadOnlySpan, attrs []attribute.KeyValue) bool {
	rate := a.cfg.SampleRate
	if cb := a.cfg.SampleOnRequest; cb != nil {
		view := &exportCopy{ReadOnlySpan: live, attrs: mergeAttrs(live.Attributes(), attrs), kind: live.SpanKind()}
		if r, ok := callSampler(cb, view); ok {
			rate = r
		}
	}
	return keepTraceID(live.SpanContext().TraceID(), rate)
}

func callSampler(cb func(sdktrace.ReadOnlySpan) (float64, bool), s sdktrace.ReadOnlySpan) (rate float64, ok bool) {
	defer func() {
		if recover() != nil {
			rate, ok = 1, true
		}
	}()
	rate, ok = cb(s)
	if ok && !(rate >= 0 && rate <= 1) {
		return 1, true
	}
	return rate, ok
}

func keepTraceID(id trace.TraceID, rate float64) bool {
	if rate >= 1 {
		return true
	}
	return binary.BigEndian.Uint64(id[8:16])>>1 < uint64(rate*(1<<63))
}
