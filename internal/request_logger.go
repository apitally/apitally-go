package internal

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/google/uuid"
)

const (
	maxBodySize      = 50_000    // 50 KB (uncompressed)
	maxFileSize      = 1_000_000 // 1 MB (compressed)
	maxFiles         = 50
	maxPendingWrites = 100
	masked           = "******"
)

var (
	bodyTooLarge        = []byte("<body too large>")
	allowedContentTypes = []string{"application/json", "text/plain"}

	excludePathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)/_?healthz?$`),
		regexp.MustCompile(`(?i)/_?health[_-]?checks?$`),
		regexp.MustCompile(`(?i)/_?heart[_-]?beats?$`),
		regexp.MustCompile(`(?i)/ping$`),
		regexp.MustCompile(`(?i)/ready$`),
		regexp.MustCompile(`(?i)/live$`),
	}
	excludeUserAgentPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)health[-_ ]?check`),
		regexp.MustCompile(`(?i)microsoft-azure-application-lb`),
		regexp.MustCompile(`(?i)googlehc`),
		regexp.MustCompile(`(?i)kube-probe`),
	}
	maskQueryParamPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)auth`),
		regexp.MustCompile(`(?i)api-?key`),
		regexp.MustCompile(`(?i)secret`),
		regexp.MustCompile(`(?i)token`),
		regexp.MustCompile(`(?i)password`),
		regexp.MustCompile(`(?i)pwd`),
	}
	maskHeaderPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)auth`),
		regexp.MustCompile(`(?i)api-?key`),
		regexp.MustCompile(`(?i)secret`),
		regexp.MustCompile(`(?i)token`),
		regexp.MustCompile(`(?i)cookie`),
	}
)

type RequestLogger struct {
	config        *common.RequestLoggingConfig
	enabled       bool
	suspendUntil  *time.Time
	pendingWrites chan string
	currentFile   *TempGzipFile
	files         chan *TempGzipFile
}

type logItem struct {
	UUID      string           `json:"uuid"`
	Request   *common.Request  `json:"request"`
	Response  *common.Response `json:"response"`
	Exception *exceptionInfo   `json:"exception,omitempty"`
}

type exceptionInfo struct {
	Type          string  `json:"type"`
	Message       string  `json:"message"`
	Stacktrace    string  `json:"stacktrace"`
	SentryEventID *string `json:"sentryEventId,omitempty"`
}

func NewRequestLogger(config *common.RequestLoggingConfig) *RequestLogger {
	if config == nil {
		config = &common.RequestLoggingConfig{
			LogQueryParams:     true,
			LogRequestHeaders:  false,
			LogRequestBody:     false,
			LogResponseHeaders: true,
			LogResponseBody:    false,
		}
	}

	logger := &RequestLogger{
		config:        config,
		enabled:       config.Enabled,
		pendingWrites: make(chan string, maxPendingWrites),
		files:         make(chan *TempGzipFile, maxFiles),
	}

	if logger.enabled {
		go logger.maintain()
	}

	return logger
}

func (rl *RequestLogger) LogRequest(request *common.Request, response *common.Response) {
	if !rl.enabled || (rl.suspendUntil != nil && time.Now().Before(*rl.suspendUntil)) {
		return
	}

	parsedURL, parseErr := url.Parse(request.URL)
	if parseErr != nil {
		return
	}

	var userAgent string
	for _, header := range request.Headers {
		if header[0] == "User-Agent" {
			userAgent = header[1]
			break
		}
	}

	if rl.shouldExcludePath(request.Path) || rl.shouldExcludeUserAgent(userAgent) {
		return
	}

	if rl.config.ExcludeCallback != nil && rl.config.ExcludeCallback(request, response) {
		return
	}

	// Process query params
	if rl.config.LogQueryParams {
		parsedURL.RawQuery = rl.maskQueryParams(parsedURL.RawQuery)
	} else {
		parsedURL.RawQuery = ""
	}
	request.URL = parsedURL.String()

	// Process request body
	if !rl.config.LogRequestBody || !rl.hasSupportedContentType(request.Headers) {
		request.Body = nil
	} else if request.Body != nil {
		if len(request.Body) > maxBodySize {
			request.Body = bodyTooLarge
		} else if rl.config.MaskRequestBodyCallback != nil {
			request.Body = rl.config.MaskRequestBodyCallback(request)
			if len(request.Body) > maxBodySize {
				request.Body = bodyTooLarge
			}
		}
	}

	// Process response body
	if !rl.config.LogResponseBody || !rl.hasSupportedContentType(response.Headers) {
		response.Body = nil
	} else if response.Body != nil {
		if len(response.Body) > maxBodySize {
			response.Body = bodyTooLarge
		} else if rl.config.MaskResponseBodyCallback != nil {
			response.Body = rl.config.MaskResponseBodyCallback(request, response)
			if len(response.Body) > maxBodySize {
				response.Body = bodyTooLarge
			}
		}
	}

	// Process headers
	if !rl.config.LogRequestHeaders {
		request.Headers = nil
	} else {
		request.Headers = rl.maskHeaders(request.Headers)
	}
	if !rl.config.LogResponseHeaders {
		response.Headers = nil
	} else {
		response.Headers = rl.maskHeaders(response.Headers)
	}

	item := logItem{
		UUID:     uuid.New().String(),
		Request:  request,
		Response: response,
	}

	jsonData, err := json.Marshal(item)
	if err != nil {
		return
	}

	// Non-blocking send to channel
	select {
	case rl.pendingWrites <- string(jsonData):
	default:
		// Channel is full, drop the oldest item and try again
		select {
		case <-rl.pendingWrites:
			rl.pendingWrites <- string(jsonData)
		default:
		}
	}
}

func (rl *RequestLogger) writeToFile() error {
	if !rl.enabled {
		return nil
	}

	// Non-blocking check if there are pending writes
	select {
	case item := <-rl.pendingWrites:
		if rl.currentFile == nil {
			var err error
			rl.currentFile, err = NewTempGzipFile()
			if err != nil {
				return err
			}
		}

		if err := rl.currentFile.WriteLine([]byte(item)); err != nil {
			return err
		}

		// Process any remaining items
		for len(rl.pendingWrites) > 0 {
			item := <-rl.pendingWrites
			if err := rl.currentFile.WriteLine([]byte(item)); err != nil {
				return err
			}
		}
	default:
		return nil
	}

	return nil
}

func (rl *RequestLogger) GetFile() *TempGzipFile {
	select {
	case file := <-rl.files:
		return file
	default:
		return nil
	}
}

func (rl *RequestLogger) RetryFileLater(file *TempGzipFile) {
	// Non-blocking send to channel
	select {
	case rl.files <- file:
	default:
		// If channel is full, delete the file
		_ = file.Delete()
	}
}

func (rl *RequestLogger) rotateFile() error {
	if rl.currentFile != nil {
		if err := rl.currentFile.Close(); err != nil {
			return err
		}
		// Non-blocking send to channel
		select {
		case rl.files <- rl.currentFile:
		default:
			// If channel is full, delete the oldest file and try again
			select {
			case oldFile := <-rl.files:
				_ = oldFile.Delete()
				rl.files <- rl.currentFile
			default:
				_ = rl.currentFile.Delete()
			}
		}
		rl.currentFile = nil
	}
	return nil
}

func (rl *RequestLogger) maintain() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if !rl.enabled {
			return
		}

		if err := rl.writeToFile(); err != nil {
			continue
		}

		if rl.currentFile != nil && rl.currentFile.Size() > maxFileSize {
			if err := rl.rotateFile(); err != nil {
				continue
			}
		}

		// Clean up excess files
		for len(rl.files) > maxFiles {
			file := <-rl.files
			_ = file.Delete()
		}

		if rl.suspendUntil != nil && time.Now().After(*rl.suspendUntil) {
			rl.suspendUntil = nil
		}
	}
}

func (rl *RequestLogger) Clear() error {
	// Drain and delete all pending writes
	for len(rl.pendingWrites) > 0 {
		<-rl.pendingWrites
	}

	if err := rl.rotateFile(); err != nil {
		return err
	}

	// Drain and delete all files
	for len(rl.files) > 0 {
		file := <-rl.files
		if err := file.Delete(); err != nil {
			return err
		}
	}
	return nil
}

func (rl *RequestLogger) Close() error {
	rl.enabled = false
	return rl.Clear()
}

func (rl *RequestLogger) shouldExcludePath(urlPath string) bool {
	for _, pattern := range excludePathPatterns {
		if pattern.MatchString(urlPath) {
			return true
		}
	}
	for _, pattern := range rl.config.ExcludePaths {
		if pattern.MatchString(urlPath) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) shouldExcludeUserAgent(userAgent string) bool {
	if userAgent == "" {
		return false
	}
	for _, pattern := range excludeUserAgentPatterns {
		if pattern.MatchString(userAgent) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) shouldMaskQueryParam(name string) bool {
	for _, pattern := range maskQueryParamPatterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	for _, pattern := range rl.config.MaskQueryParams {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) shouldMaskHeader(name string) bool {
	for _, pattern := range maskHeaderPatterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	for _, pattern := range rl.config.MaskHeaders {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) hasSupportedContentType(headers [][2]string) bool {
	for _, header := range headers {
		if header[0] == "Content-Type" {
			return rl.isSupportedContentType(header[1])
		}
	}
	return false
}

func (rl *RequestLogger) isSupportedContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	for _, allowed := range allowedContentTypes {
		if bytes.HasPrefix([]byte(contentType), []byte(allowed)) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) maskQueryParams(search string) string {
	params, err := url.ParseQuery(search)
	if err != nil {
		return search
	}
	for key := range params {
		if rl.shouldMaskQueryParam(key) {
			params.Set(key, masked)
		}
	}
	return params.Encode()
}

func (rl *RequestLogger) maskHeaders(headers [][2]string) [][2]string {
	result := make([][2]string, len(headers))
	for i, header := range headers {
		if rl.shouldMaskHeader(header[0]) {
			result[i] = [2]string{header[0], masked}
		} else {
			result[i] = header
		}
	}
	return result
}
