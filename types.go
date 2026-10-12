// Package apitally defines the configuration and data types of the Apitally
// SDK. Applications import the package for their framework instead, which
// re-exports these types:
//
//   - github.com/apitally/apitally-go/chi-v5
//   - github.com/apitally/apitally-go/echo-v4
//   - github.com/apitally/apitally-go/echo-v5
//   - github.com/apitally/apitally-go/fiber-v2
//   - github.com/apitally/apitally-go/fiber-v3
//   - github.com/apitally/apitally-go/gin-v1
package apitally

import (
	"log/slog"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config configures Apitally. Create it with [NewConfig], which sets the
// defaults.
type Config struct {
	// WriteToken is the write token shown in the setup instructions of the
	// app in the Apitally dashboard. Defaults to the APITALLY_WRITE_TOKEN
	// environment variable.
	WriteToken string

	// Env is the name of the environment, such as "prod" or "staging".
	// Defaults to the APITALLY_ENV environment variable, or "dev" if unset.
	Env string

	// AppVersion is the version of the application, such as "1.4.2".
	AppVersion string

	// Disabled turns Apitally off. Setting the APITALLY_DISABLED or
	// OTEL_SDK_DISABLED environment variable to 1, true or yes also turns it
	// off.
	Disabled bool

	// CaptureRequestHeaders includes request headers in request logs.
	// Defaults to false.
	CaptureRequestHeaders bool

	// CaptureRequestBody includes JSON and text request bodies up to 50,000
	// bytes in request logs. Defaults to false.
	CaptureRequestBody bool

	// CaptureResponseHeaders includes response headers in request logs.
	// Defaults to true.
	CaptureResponseHeaders bool

	// CaptureResponseBody includes JSON and text response bodies up to 50,000
	// bytes in request logs. Defaults to false.
	CaptureResponseBody bool

	// SampleRate is the fraction of requests to include in request logs and
	// traces, from 0 to 1. Sampling does not affect metrics and error
	// tracking. Defaults to 1.
	SampleRate float64

	// SampleOnRequest, if set, is called when a request starts and returns
	// the request's sample rate, which overrides SampleRate. Return ok ==
	// false to use SampleRate.
	SampleOnRequest func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)

	// SampleOnResponse, if set, is called after the request's span has ended
	// and returns the request's sample rate. It can lower the rate that
	// applied when the request started, but not raise it. Return ok == false
	// to keep that rate.
	SampleOnResponse func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)

	// MaskRequestBody, if set, returns the request body to capture in place
	// of body, which is already decompressed. Returning nil or an empty slice
	// captures "[REDACTED]".
	MaskRequestBody func(span sdktrace.ReadOnlySpan, body []byte) []byte

	// MaskResponseBody, if set, returns the response body to capture in place
	// of body, which is already decompressed. Returning nil or an empty slice
	// captures "[REDACTED]".
	MaskResponseBody func(span sdktrace.ReadOnlySpan, body []byte) []byte

	// MaskLogRecord, if set, can modify a captured log record before it is
	// sent to Apitally. Return false to drop the record.
	MaskLogRecord func(record *LogRecord) bool

	// MaskQueryParams lists regular expressions for query parameter names
	// whose values are replaced with "[REDACTED]". Common sensitive names,
	// such as token and password, are masked by default. Patterns are
	// case-insensitive and match anywhere in the name.
	MaskQueryParams []string

	// MaskHeaders lists regular expressions for header names whose values
	// are replaced with "[REDACTED]". Common sensitive headers, such as
	// Authorization and Cookie, are masked by default. Patterns are
	// case-insensitive and match anywhere in the name.
	MaskHeaders []string

	// MaskBodyFields lists regular expressions for JSON body field names
	// whose string values are replaced with "[REDACTED]". Common sensitive
	// fields, such as password and token, are masked by default. Patterns are
	// case-insensitive and match anywhere in the name.
	MaskBodyFields []string

	// ExcludePaths lists regular expressions for request paths to exclude
	// from request logs and traces. Common health check and static asset
	// paths, such as /healthz and /robots.txt, are excluded by default.
	// Metrics still include excluded requests. Patterns are case-insensitive
	// and match anywhere in the path.
	ExcludePaths []string
}

// NewConfig returns a Config with the default settings.
func NewConfig() *Config {
	return &Config{
		CaptureResponseHeaders: true,
		SampleRate:             1,
	}
}

// Consumer identifies the API consumer making a request.
type Consumer struct {
	// Identifier uniquely identifies the consumer, such as a user or
	// customer ID. Required. Truncated to 128 characters.
	Identifier string

	// Name is an optional display name. Truncated to 64 characters.
	Name string

	// Group is an optional group name. Truncated to 64 characters.
	Group string

	// Attributes are optional custom attributes.
	Attributes map[string]string
}

// LogRecord is a log record captured by the handler from NewSlogHandler, as
// passed to Config.MaskLogRecord.
type LogRecord struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   []slog.Attr
}
