package internal

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// startServerSpan reuses a SERVER span that the application's own HTTP
// instrumentation started outside Apitally's middleware, or starts one. The
// started span is a child of the incoming context, or of the upstream context
// in the request headers.
func (r *sdkRuntime) startServerSpan(ctx context.Context, info *RequestInfo, attrs []attribute.KeyValue) (context.Context, trace.Span, bool) {
	if span := trace.SpanFromContext(ctx); r.isReusable(span) {
		return ctx, span, false
	}
	parentCtx := ctx
	if !trace.SpanContextFromContext(ctx).IsValid() {
		parentCtx = propagator().Extract(ctx, propagation.HeaderCarrier(info.Header))
	}
	spanCtx, span := r.tracer.Start(parentCtx, info.Method, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
	// A private provider's span stays out of the request context, so spans of
	// the application's provider never reference a span its backend lacks.
	if r.isProviderPrivate {
		return ctx, span, true
	}
	return spanCtx, span, true
}

// isReusable reports whether span is a recording SERVER span from the
// provider carrying Apitally's span processor that no monitored request owns.
func (r *sdkRuntime) isReusable(span trace.Span) bool {
	readOnly, ok := span.(sdktrace.ReadOnlySpan)
	return ok && span.IsRecording() && readOnly.SpanKind() == trace.SpanKindServer &&
		span.TracerProvider() == trace.TracerProvider(r.provider) && !r.registry.isRegistered(span.SpanContext().SpanID())
}

// finishServerSpan sets the transport attributes on a span Apitally started
// and ends it at the observation boundary.
func finishServerSpan(span trace.Span, method, route string, status int, attrs []attribute.KeyValue, end time.Time) {
	if route != "" {
		span.SetName(method + " " + route)
	}
	span.SetAttributes(attrs...)
	if status >= 500 {
		span.SetStatus(codes.Error, "")
	}
	span.End(trace.WithTimestamp(end))
}

// requestAttributes returns the stable HTTP semantic convention attributes
// known at middleware entry.
func requestAttributes(info *RequestInfo) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", info.Method),
		attribute.String("url.scheme", info.Scheme),
		attribute.String("url.path", toValidUTF8(info.Path)),
	}
	host, port := splitHostPort(info.Host)
	if host != "" {
		attrs = append(attrs, attribute.String("server.address", host))
	}
	if port > 0 {
		attrs = append(attrs, attribute.Int("server.port", port))
	}
	if info.Query != "" {
		attrs = append(attrs, attribute.String("url.query", toValidUTF8(info.Query)))
	}
	if userAgent := info.Header.Get("User-Agent"); userAgent != "" {
		attrs = append(attrs, attribute.String("user_agent.original", toValidUTF8(userAgent)))
	}
	return attrs
}

// transportAttributes returns the attributes known when observation ends.
func transportAttributes(result *TransportResult) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.Int("http.response.status_code", result.StatusCode)}
	if result.Route != "" {
		attrs = append(attrs, attribute.String("http.route", result.Route))
	}
	if result.ClientAddress != "" {
		attrs = append(attrs, attribute.String("client.address", result.ClientAddress))
	}
	return attrs
}

// mergeAttributes returns a new slice in which later attributes replace
// earlier ones with the same key.
func mergeAttributes(sets ...[]attribute.KeyValue) []attribute.KeyValue {
	var out []attribute.KeyValue
	index := map[attribute.Key]int{}
	for _, set := range sets {
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

// HostFromAddress returns the host of a host:port address, or the address
// itself when it has no port.
func HostFromAddress(address string) string {
	host, _ := splitHostPort(address)
	return host
}

func splitHostPort(address string) (string, int) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return strings.Trim(address, "[]"), 0
	}
	port, _ := strconv.Atoi(portText)
	return host, port
}

// toValidUTF8 replaces invalid UTF-8, which would make the whole export
// unparseable at ingest.
func toValidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}
