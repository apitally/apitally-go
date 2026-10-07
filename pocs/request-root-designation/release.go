package rootpoc

import (
	"sync"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const maxBufferedSpans = 1000

type request struct {
	a        *Apitally
	root     trace.SpanID
	reqAttrs []attribute.KeyValue
	members  []trace.SpanID // guarded by processor.mu

	mu        sync.Mutex
	spans     []sdktrace.ReadOnlySpan
	rootSpan  sdktrace.ReadOnlySpan
	respAttrs []attribute.KeyValue
	observed  bool
	released  bool
}

func (r *request) spanEnded(s sdktrace.ReadOnlySpan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.released:
	case s.SpanContext().SpanID() == r.root:
		r.rootSpan = s
		r.releaseIfDone("root-end")
	case len(r.spans) < maxBufferedSpans:
		r.spans = append(r.spans, s)
	}
}

func (r *request) observe(attrs []attribute.KeyValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.respAttrs, r.observed = attrs, true
	r.releaseIfDone("observation")
}

func (r *request) releaseIfDone(last string) {
	if !r.observed || r.rootSpan == nil {
		return
	}
	r.released = true
	r.a.requests.remove(r)
	out := make([]sdktrace.ReadOnlySpan, 0, len(r.spans)+1)
	for _, s := range append(r.spans, r.rootSpan) {
		if s.SpanContext().IsSampled() {
			out = append(out, newExportCopy(s, r.root, r.reqAttrs, r.respAttrs))
		}
	}
	r.spans = nil
	if len(out) > 0 {
		r.a.export(Release{Spans: out, LastEvent: last})
	}
}
