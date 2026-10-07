package internal

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
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
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	attrs := make([]attribute.KeyValue, 0, len(names))
	for _, name := range names {
		values := make([]string, len(header[name]))
		for i, value := range header[name] {
			values[i] = red.redactHeaderValue(name, value)
		}
		if red.isHeaderRedacted(name) {
			values = []string{redactedValue}
		}
		attrs = append(attrs, attribute.StringSlice(prefix+strings.ToLower(name), values))
	}
	return attrs
}

// redactHeaderValue redacts the query of Location and Content-Location
// values, which are URLs.
func (red *redaction) redactHeaderValue(name, value string) string {
	switch strings.ToLower(strings.ReplaceAll(name, "_", "-")) {
	case "location", "content-location":
		return red.redactURLQuery(value)
	}
	return value
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
		case isHeader && red.isHeaderRedacted(header):
			value = redactedValue
		case isHeader:
			value = red.redactHeaderValue(header, value)
		case key == "url.query":
			value = red.redactQuery(value)
		case key == "url.full" || key == "http.url" || key == "http.target":
			value = red.redactURLQuery(value)
		}
		return attribute.StringValue(value), value != original
	case attribute.STRINGSLICE:
		original := kv.Value.AsStringSlice()
		if isHeader && red.isHeaderRedacted(header) {
			return attribute.StringSliceValue([]string{redactedValue}), true
		}
		values := make([]string, len(original))
		isChanged := false
		for i, item := range original {
			values[i] = item
			if isHeader {
				values[i] = red.redactHeaderValue(header, item)
			}
			isChanged = isChanged || values[i] != item
		}
		return attribute.StringSliceValue(values), isChanged
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
