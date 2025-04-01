package common

import "regexp"

// Request represents an HTTP request being logged
type Request struct {
	Timestamp float64     `json:"timestamp"`
	Method    string      `json:"method"`
	Path      string      `json:"path,omitempty"`
	URL       string      `json:"url"`
	Headers   [][2]string `json:"headers"`
	Size      *int64      `json:"size,omitempty"`
	Consumer  *string     `json:"consumer,omitempty"`
	Body      []byte      `json:"body,omitempty"`
}

// Response represents an HTTP response being logged
type Response struct {
	StatusCode   int         `json:"status_code"`
	ResponseTime float64     `json:"response_time"`
	Headers      [][2]string `json:"headers"`
	Size         *int64      `json:"size,omitempty"`
	Body         []byte      `json:"body,omitempty"`
}

// ApitallyConsumer represents a consumer of the API
type ApitallyConsumer struct {
	Identifier string  `json:"identifier"`
	Name       *string `json:"name,omitempty"`
	Group      *string `json:"group,omitempty"`
}

// PathInfo represents a path and method pair
type PathInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// RequestLoggingConfig defines configuration for request logging
type RequestLoggingConfig struct {
	Enabled                  bool
	LogQueryParams           bool
	LogRequestHeaders        bool
	LogRequestBody           bool
	LogResponseHeaders       bool
	LogResponseBody          bool
	LogException             bool
	MaskQueryParams          []*regexp.Regexp
	MaskHeaders              []*regexp.Regexp
	MaskRequestBodyCallback  func(request *Request) []byte
	MaskResponseBodyCallback func(request *Request, response *Response) []byte
	ExcludePaths             []*regexp.Regexp
	ExcludeCallback          func(request *Request, response *Response) bool
}

// ApitallyConfig defines the configuration for Apitally
type ApitallyConfig struct {
	ClientId             string
	Env                  string
	RequestLoggingConfig *RequestLoggingConfig
	AppVersion           *string
}
