package internal

import (
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

const (
	redactedValue        = "[REDACTED]"
	requestHeaderPrefix  = "http.request.header."
	responseHeaderPrefix = "http.response.header."
)

var (
	defaultMaskQueryParamPatterns = compileDefaultPatterns(`auth`, `api[-_]?key`, `secret`, `token`, `password`, `pwd`)
	defaultMaskHeaderPatterns     = compileDefaultPatterns(`auth`, `api[-_]?key`, `secret`, `token`, `cookie`)
	defaultMaskBodyFieldPatterns  = compileDefaultPatterns(`password`, `pwd`, `token`, `secret`, `auth`, `card[-_ ]?number`, `ccv`, `cvv`, `cvc`, `ssn`)
)

// redaction applies the default patterns together with the user's patterns.
type redaction struct {
	queryParams []*regexp.Regexp
	headers     []*regexp.Regexp
	bodyFields  []*regexp.Regexp
}

func newRedaction(s *settings) *redaction {
	return &redaction{
		queryParams: slices.Concat(defaultMaskQueryParamPatterns, s.maskQueryParams),
		headers:     slices.Concat(defaultMaskHeaderPatterns, s.maskHeaders),
		bodyFields:  slices.Concat(defaultMaskBodyFieldPatterns, s.maskBodyFields),
	}
}

// redactQuery replaces the values of parameters whose percent-decoded names
// match in a raw query string.
func (red *redaction) redactQuery(query string) string {
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		name, _, hasValue := strings.Cut(pair, "=")
		if hasValue && matchesAny(red.queryParams, decodeQueryParamName(name)) {
			pairs[i] = name + "=" + redactedValue
		}
	}
	return strings.Join(pairs, "&")
}

// decodeQueryParamName decodes the valid percent escapes of a parameter name
// and keeps malformed ones, so a malformed escape cannot hide the name from
// redaction.
func decodeQueryParamName(name string) string {
	var escaped strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '%' && (i+2 >= len(name) || !isHexDigit(name[i+1]) || !isHexDigit(name[i+2])) {
			escaped.WriteString("%25")
		} else {
			escaped.WriteByte(name[i])
		}
	}
	decoded, _ := url.QueryUnescape(escaped.String())
	return decoded
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// redactURLQuery redacts the query of a URL or request target.
func (red *redaction) redactURLQuery(value string) string {
	base, query, hasQuery := strings.Cut(value, "?")
	if !hasQuery {
		return value
	}
	return base + "?" + red.redactQuery(query)
}

// isHeaderRedacted also matches the underscore form of header names that
// older instrumentation uses in attribute keys.
func (red *redaction) isHeaderRedacted(name string) bool {
	return matchesAny(red.headers, name) || matchesAny(red.headers, strings.ReplaceAll(name, "_", "-"))
}

func (red *redaction) isBodyFieldRedacted(name string) bool {
	return matchesAny(red.bodyFields, name)
}

// headerAttributes returns captured headers as list-valued attributes with
// lowercase names, sorted by name. A redacted header has one value.
func (red *redaction) headerAttributes(prefix string, header http.Header) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(header))
	for _, name := range slices.Sorted(maps.Keys(header)) {
		attrs = append(attrs, attribute.StringSlice(prefix+strings.ToLower(name), red.redactHeaderValues(name, header[name])))
	}
	return attrs
}

// redactHeaderValues returns one value for a redacted header, and redacts
// the query of Location and Content-Location values, which are URLs.
func (red *redaction) redactHeaderValues(name string, values []string) []string {
	if red.isHeaderRedacted(name) {
		return []string{redactedValue}
	}
	switch strings.ToLower(strings.ReplaceAll(name, "_", "-")) {
	case "location", "content-location":
		redacted := make([]string, len(values))
		for i, value := range values {
			redacted[i] = red.redactURLQuery(value)
		}
		return redacted
	}
	return values
}

// redactSpanAttributes redacts query-bearing attributes and captured header
// attributes of stable and legacy semantic conventions, including those set
// by the application's instrumentation. It returns attrs itself when nothing
// changes.
func (red *redaction) redactSpanAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := attrs
	for i, kv := range attrs {
		value, isChanged := red.redactSpanAttribute(kv)
		if !isChanged {
			continue
		}
		if &out[0] == &attrs[0] {
			out = slices.Clone(attrs)
		}
		out[i] = attribute.KeyValue{Key: kv.Key, Value: value}
	}
	return out
}

func (red *redaction) redactSpanAttribute(kv attribute.KeyValue) (attribute.Value, bool) {
	key := string(kv.Key)
	header, isHeader := strings.CutPrefix(key, requestHeaderPrefix)
	if !isHeader {
		header, isHeader = strings.CutPrefix(key, responseHeaderPrefix)
	}
	switch kv.Value.Type() {
	case attribute.STRING:
		original := kv.Value.AsString()
		value := original
		switch {
		case isHeader:
			value = red.redactHeaderValues(header, []string{value})[0]
		case key == "url.query":
			value = red.redactQuery(value)
		case key == "url.full" || key == "http.url" || key == "http.target":
			value = red.redactURLQuery(value)
		}
		return attribute.StringValue(value), value != original
	case attribute.STRINGSLICE:
		if !isHeader {
			return kv.Value, false
		}
		original := kv.Value.AsStringSlice()
		values := red.redactHeaderValues(header, original)
		return attribute.StringSliceValue(values), !slices.Equal(values, original)
	}
	return kv.Value, false
}

func compileDefaultPatterns(patterns ...string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		compiled[i] = regexp.MustCompile("(?i)" + pattern)
	}
	return compiled
}
