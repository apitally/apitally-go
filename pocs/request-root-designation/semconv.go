package rootpoc

import (
	"go.opentelemetry.io/otel/attribute"
)

type RequestInfo struct {
	Method, Scheme, Path, Route, UserAgent string
}

func (i RequestInfo) attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", i.Method),
		attribute.String("url.scheme", i.Scheme),
		attribute.String("url.path", i.Path),
		attribute.String("user_agent.original", i.UserAgent),
	}
	if i.Route != "" {
		attrs = append(attrs, attribute.String("http.route", i.Route))
	}
	return attrs
}

func responseAttributes(status, size int, route, contentType string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int("http.response.status_code", status),
		attribute.Int("http.response.body.size", max(size, 0)),
		attribute.StringSlice("http.response.header.content-type", []string{contentType}),
	}
	if route != "" {
		attrs = append(attrs, attribute.String("http.route", route))
	}
	return attrs
}

type headerGetter func(string) string

func (g headerGetter) Get(key string) string { return g(key) }
func (headerGetter) Set(string, string)      {}
func (headerGetter) Keys() []string          { return nil }
