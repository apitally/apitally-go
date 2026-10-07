package internal

import (
	"go.opentelemetry.io/otel/attribute"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const redactedValue = "[REDACTED]"

var defaultMaskQueryParamPatterns = compileDefaultPatterns(`auth`, `api[-_]?key`, `secret`, `token`, `password`, `pwd`)

// redaction applies the default patterns together with the user's patterns.
type redaction struct {
	queryParams []*regexp.Regexp
}

func newRedaction(s *settings) *redaction {
	return &redaction{
		queryParams: slices.Concat(defaultMaskQueryParamPatterns, s.maskQueryParams),
	}
}

// redactQuery replaces the values of parameters whose percent-decoded names
// match in a raw query string. A malformed escape is matched undecoded.
func (red *redaction) redactQuery(query string) string {
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		name, _, hasValue := strings.Cut(pair, "=")
		if !hasValue {
			continue
		}
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if matchesAny(red.queryParams, decoded) {
			pairs[i] = name + "=" + redactedValue
		}
	}
	return strings.Join(pairs, "&")
}

// redactURLQuery redacts the query of a URL or request target.
func (red *redaction) redactURLQuery(value string) string {
	base, query, hasQuery := strings.Cut(value, "?")
	if !hasQuery {
		return value
	}
	return base + "?" + red.redactQuery(query)
}

// redactSpanAttributes redacts the query in query-bearing attributes of
// stable and legacy semantic conventions and replaces invalid UTF-8. It
// returns attrs itself when nothing changes.
func (red *redaction) redactSpanAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := attrs
	for i, kv := range attrs {
		if kv.Value.Type() != attribute.STRING {
			continue
		}
		original := kv.Value.AsString()
		value := toValidUTF8(original)
		switch kv.Key {
		case "url.query":
			value = red.redactQuery(value)
		case "url.full", "http.url", "http.target":
			value = red.redactURLQuery(value)
		}
		if value == original {
			continue
		}
		if &out[0] == &attrs[0] {
			out = slices.Clone(attrs)
		}
		out[i] = attribute.String(string(kv.Key), value)
	}
	return out
}

func compileDefaultPatterns(patterns ...string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		compiled[i] = regexp.MustCompile("(?i)" + pattern)
	}
	return compiled
}
