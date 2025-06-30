package common

import "regexp"

type Request struct {
	Timestamp float64     `json:"timestamp"`
	Method    string      `json:"method"`
	Path      string      `json:"path,omitempty"`
	URL       string      `json:"url"`
	Headers   [][2]string `json:"headers"`
	Size      int64       `json:"size,omitempty"`
	Consumer  string      `json:"consumer,omitempty"`
	Body      []byte      `json:"body,omitempty"`
}

type Response struct {
	StatusCode   int         `json:"status_code"`
	ResponseTime float64     `json:"response_time"`
	Headers      [][2]string `json:"headers"`
	Size         int64       `json:"size,omitempty"`
	Body         []byte      `json:"body,omitempty"`
}

type Consumer struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name,omitempty"`
	Group      string `json:"group,omitempty"`
}

type PathInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type RequestLoggingConfig struct {
	Enabled                  bool
	LogQueryParams           bool
	LogRequestHeaders        bool
	LogRequestBody           bool
	LogResponseHeaders       bool
	LogResponseBody          bool
	LogPanic                 bool
	MaskQueryParams          []*regexp.Regexp
	MaskHeaders              []*regexp.Regexp
	MaskBodyFields           []*regexp.Regexp
	MaskRequestBodyCallback  func(request *Request) []byte
	MaskResponseBodyCallback func(request *Request, response *Response) []byte
	ExcludePaths             []*regexp.Regexp
	ExcludeCallback          func(request *Request, response *Response) bool
}

func NewRequestLoggingConfig() *RequestLoggingConfig {
	return &RequestLoggingConfig{
		Enabled:            false,
		LogQueryParams:     true,
		LogRequestHeaders:  false,
		LogRequestBody:     false,
		LogResponseHeaders: true,
		LogResponseBody:    false,
		LogPanic:           true,
	}
}

type Config struct {
	ClientID       string
	Env            string
	AppVersion     string
	RequestLogging *RequestLoggingConfig

	// For testing purposes
	DisableSync bool
}

// NewConfig creates a new Apitally configuration with sensible defaults.
//
// See reference: https://docs.apitally.io/reference/go
func NewConfig(clientID string) *Config {
	return &Config{
		ClientID:       clientID,
		Env:            "dev",
		RequestLogging: NewRequestLoggingConfig(),
	}
}
