package internal

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spanProcessor links spans to the monitored request of their local parent
// and buffers them in the request when they end. Spans of no monitored
// request are dropped, so only request-rooted traces reach Apitally.
type spanProcessor struct {
	registry *requestRegistry
}

func (p *spanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	defer recoverAndLogPanic("span start")
	if parent := span.Parent(); parent.IsValid() && !parent.IsRemote() {
		p.registry.link(parent.SpanID(), span.SpanContext().SpanID())
	}
}

func (p *spanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	defer recoverAndLogPanic("span end")
	if state := p.registry.lookup(span.SpanContext().SpanID()); state != nil {
		state.spanEnded(span)
	}
}

func (p *spanProcessor) Shutdown(context.Context) error   { return nil }
func (p *spanProcessor) ForceFlush(context.Context) error { return nil }
