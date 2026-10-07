// Package apitally declares the configuration and data types shared by the
// Apitally framework integrations.
//
// Applications import the package of their framework instead, such as
// github.com/apitally/apitally-go/gin-v1, which re-exports these types.
package apitally

import (
	"log/slog"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config configures Apitally. Create it with [NewConfig], which sets the
// defaults that are not Go zero values.
type Config struct {
	// WriteToken authenticates exports to Apitally. Defaults to the
	// APITALLY_WRITE_TOKEN environment variable. Without a valid token,
	// Apitally logs an error and stays disabled.
	WriteToken string

	// Env is the environment name, such as "prod" or "staging". Defaults to
	// the APITALLY_ENV environment variable, then "dev".
	Env string

	// AppVersion is the application version reported at startup.
	AppVersion string

	// Disabled turns Apitally off. Apitally is also disabled when the
	// APITALLY_DISABLED or OTEL_SDK_DISABLED environment variable is set to
	// 1, true or yes.
	Disabled bool

	// CaptureLogs captures request-scoped application logs written through a
	// handler created with NewSlogHandler. Defaults to true.
	CaptureLogs bool

	// CaptureRequestHeaders captures request headers. Defaults to false.
	CaptureRequestHeaders bool

	// CaptureRequestBody captures request bodies with a supported content
	// type up to 50,000 bytes. Defaults to false.
	CaptureRequestBody bool

	// CaptureResponseHeaders captures response headers. Defaults to true.
	CaptureResponseHeaders bool

	// CaptureResponseBody captures response bodies with a supported content
	// type up to 50,000 bytes. Defaults to false.
	CaptureResponseBody bool

	// SampleRate is the fraction of requests whose traces, logs, headers and
	// bodies are captured, between 0 and 1. Request metrics and errors are
	// always recorded. Defaults to 1. Invalid values resolve to 1.
	SampleRate float64

	// SampleOnRequest returns the sample rate for a request when it starts,
	// replacing SampleRate. Returning ok == false keeps SampleRate. The span
	// is read-only.
	SampleOnRequest func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)

	// SampleOnResponse returns the sample rate for a request after its
	// response, which can only lower the earlier rate. Returning ok == false
	// keeps the earlier decision. It may run later, on another goroutine,
	// against the ended span, which is read-only.
	SampleOnResponse func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)

	// MaskRequestBody returns the request body to capture in place of body,
	// which is already decompressed. Returning nil or an empty slice captures
	// "[REDACTED]". It may run later, on another goroutine, against the ended
	// span, which is read-only.
	MaskRequestBody func(span sdktrace.ReadOnlySpan, body []byte) []byte

	// MaskResponseBody returns the response body to capture in place of
	// body, which is already decompressed. Returning nil or an empty slice
	// captures "[REDACTED]". It may run later, on another goroutine, against
	// the ended span, which is read-only.
	MaskResponseBody func(span sdktrace.ReadOnlySpan, body []byte) []byte

	// MaskLogRecord can modify a captured log record in place before it is
	// exported. Returning false drops the record. It does not affect the
	// application's own log output.
	MaskLogRecord func(record *LogRecord) bool

	// MaskQueryParams lists additional regular expressions for query
	// parameter names whose values are replaced with "[REDACTED]".
	// Patterns are case-insensitive unless they set their own flags, such as
	// (?-i:...).
	MaskQueryParams []string

	// MaskHeaders lists additional regular expressions for header names
	// whose values are replaced with "[REDACTED]". Patterns are
	// case-insensitive unless they set their own flags.
	MaskHeaders []string

	// MaskBodyFields lists additional regular expressions for JSON body field
	// names whose string values are replaced with "[REDACTED]". Patterns are
	// case-insensitive unless they set their own flags.
	MaskBodyFields []string

	// ExcludePaths lists additional regular expressions for request paths
	// whose traces and logs are not captured. Patterns are case-insensitive
	// unless they set their own flags.
	ExcludePaths []string
}

// NewConfig returns a Config with the default settings.
func NewConfig() *Config {
	return &Config{
		CaptureLogs:            true,
		CaptureResponseHeaders: true,
		SampleRate:             1,
	}
}

// Consumer identifies the API consumer making a request.
type Consumer struct {
	// Identifier identifies the consumer, such as a user or customer ID.
	// Required, at most 128 characters.
	Identifier string

	// Name is an optional display name, at most 64 characters.
	Name string

	// Group is an optional group name, at most 64 characters.
	Group string

	// Attributes are optional custom attributes. Up to 10 entries are taken
	// in key order. An empty value deletes the attribute.
	Attributes map[string]string
}

// LogRecord is a captured application log record passed to
// Config.MaskLogRecord.
type LogRecord struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   []slog.Attr
}
