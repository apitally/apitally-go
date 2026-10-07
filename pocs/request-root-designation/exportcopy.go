package rootpoc

import (
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type exportCopy struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
	kind  trace.SpanKind
}

func (c *exportCopy) Attributes() []attribute.KeyValue { return c.attrs }
func (c *exportCopy) SpanKind() trace.SpanKind         { return c.kind }

func newExportCopy(s sdktrace.ReadOnlySpan, root trace.SpanID, reqAttrs, respAttrs []attribute.KeyValue) *exportCopy {
	if s.SpanContext().SpanID() != root {
		kind := s.SpanKind()
		if kind == trace.SpanKindServer {
			kind = trace.SpanKindInternal
		}
		return &exportCopy{ReadOnlySpan: s, attrs: mergeAttrs(s.Attributes()), kind: kind}
	}
	return &exportCopy{ReadOnlySpan: s, attrs: mergeAttrs(s.Attributes(), reqAttrs, respAttrs), kind: s.SpanKind()}
}

func mergeAttrs(base []attribute.KeyValue, overrides ...[]attribute.KeyValue) []attribute.KeyValue {
	index := make(map[attribute.Key]int, len(base))
	out := make([]attribute.KeyValue, 0, len(base))
	for _, set := range append([][]attribute.KeyValue{base}, overrides...) {
		for _, kv := range set {
			if i, ok := index[kv.Key]; ok {
				out[i] = kv
				continue
			}
			index[kv.Key] = len(out)
			out = append(out, kv)
		}
	}
	return out
}
