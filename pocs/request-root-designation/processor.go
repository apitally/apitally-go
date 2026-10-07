package rootpoc

import (
	"context"
	"sync"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type processor struct {
	mu      sync.Mutex
	entries map[trace.SpanID]*request
}

func (p *processor) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	parent := s.Parent()
	if !parent.IsValid() || parent.IsRemote() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if req := p.entries[parent.SpanID()]; req != nil {
		p.add(req, s.SpanContext().SpanID())
	}
}

func (p *processor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.mu.Lock()
	req := p.entries[s.SpanContext().SpanID()]
	p.mu.Unlock()
	if req != nil {
		req.spanEnded(s)
	}
}

func (p *processor) Shutdown(context.Context) error   { return nil }
func (p *processor) ForceFlush(context.Context) error { return nil }

func (p *processor) register(req *request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.add(req, req.root)
}

func (p *processor) add(req *request, id trace.SpanID) {
	p.entries[id] = req
	req.members = append(req.members, id)
}

func (p *processor) remove(req *request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range req.members {
		if p.entries[id] == req {
			delete(p.entries, id)
		}
	}
}

func (p *processor) known(id trace.SpanID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entries[id] != nil
}

func (p *processor) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
