package internal

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/google/uuid"
)

const (
	maxFileSize      = 1_000_000 // 1 MB (compressed)
	maxFiles         = 50
	maxPendingWrites = 100
	masked           = "******"
)

var (
	bodyTooLarge        = []byte("<body too large>")
	bodyMasked          = []byte("<masked>")
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
	maskBodyFieldPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)password`),
		regexp.MustCompile(`(?i)pwd`),
		regexp.MustCompile(`(?i)token`),
		regexp.MustCompile(`(?i)secret`),
		regexp.MustCompile(`(?i)auth`),
		regexp.MustCompile(`(?i)card[-_ ]?number`),
		regexp.MustCompile(`(?i)ccv`),
		regexp.MustCompile(`(?i)ssn`),
	}
	jsonContentTypePattern = regexp.MustCompile(`(?i)\bjson\b`)
)

type RequestLogger struct {
	config           *common.RequestLoggingConfig
	enabled          bool
	enabledMutex     sync.Mutex
	suspendUntil     *time.Time
	pendingWrites    chan RequestLogItem
	currentFile      *TempGzipFile
	currentFileMutex sync.Mutex
	files            chan *TempGzipFile
	done             chan struct{}
}

type RequestLogItem struct {
	UUID      string           `json:"uuid"`
	Request   *common.Request  `json:"request"`
	Response  *common.Response `json:"response"`
	Exception *ExceptionInfo   `json:"exception,omitempty"`
}

type ExceptionInfo struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	StackTrace string `json:"stacktrace"`
}

func NewRequestLogger(config *common.RequestLoggingConfig) *RequestLogger {
	if config == nil {
		config = &common.RequestLoggingConfig{}
	}
	logger := &RequestLogger{
		config:        config,
		enabled:       config.Enabled,
		pendingWrites: make(chan RequestLogItem, maxPendingWrites),
		files:         make(chan *TempGzipFile, maxFiles),
	}
	return logger
}

func (rl *RequestLogger) IsEnabled() bool {
	rl.enabledMutex.Lock()
	defer rl.enabledMutex.Unlock()

	return rl.enabled
}

func (rl *RequestLogger) IsSuspended() bool {
	rl.enabledMutex.Lock()
	defer rl.enabledMutex.Unlock()

	return rl.suspendUntil != nil && time.Now().Before(*rl.suspendUntil)
}

func (rl *RequestLogger) SuspendFor(duration time.Duration) {
	rl.enabledMutex.Lock()
	defer rl.enabledMutex.Unlock()

	suspendTime := time.Now().Add(duration)
	rl.suspendUntil = &suspendTime
	rl.Clear()
}

func (rl *RequestLogger) StartMaintenance() {
	if rl.IsEnabled() {
		rl.done = make(chan struct{})
		go rl.maintain()
	}
}

func (rl *RequestLogger) LogRequest(request *common.Request, response *common.Response, handlerError error, stackTrace string) {
	if !rl.IsEnabled() || rl.IsSuspended() || request == nil || response == nil {
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

	if !rl.config.LogRequestBody || !rl.hasSupportedContentType(request.Headers) {
		request.Body = nil
	}
	if !rl.config.LogResponseBody || !rl.hasSupportedContentType(response.Headers) {
		response.Body = nil
	}

	item := RequestLogItem{
		UUID:     uuid.New().String(),
		Request:  request,
		Response: response,
	}

	if handlerError != nil && rl.config.LogPanic {
		errorType := getErrorType(handlerError)
		errorMessage := handlerError.Error()
		item.Exception = &ExceptionInfo{
			Type:       errorType,
			Message:    truncateExceptionMessage(errorMessage),
			StackTrace: truncateExceptionStackTrace(stackTrace),
		}
	}

	select {
	case rl.pendingWrites <- item:
	default:
		// Channel is full, drop the oldest item and try again
		select {
		case <-rl.pendingWrites:
			rl.pendingWrites <- item
		default:
		}
	}
}

// For testing purposes
func (rl *RequestLogger) GetPendingWrites() []RequestLogItem {
	result := make([]RequestLogItem, 0, len(rl.pendingWrites))
	for {
		select {
		case item := <-rl.pendingWrites:
			result = append(result, item)
		default:
			return result
		}
	}
}

func (rl *RequestLogger) writeToFile() error {
	rl.currentFileMutex.Lock()
	defer rl.currentFileMutex.Unlock()

	for {
		select {
		case item, ok := <-rl.pendingWrites:
			if !ok {
				return nil
			}
			if rl.currentFile == nil {
				var err error
				rl.currentFile, err = NewTempGzipFile()
				if err != nil {
					return err
				}
			}

			rl.applyMasking(&item)

			jsonData, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err := rl.currentFile.WriteLine(jsonData); err != nil {
				return err
			}
		default:
			// No more items to write
			return nil
		}
	}
}

func (rl *RequestLogger) applyMasking(item *RequestLogItem) {
	request := item.Request
	response := item.Response

	// Apply user-provided MaskRequestBodyCallback function
	if rl.config.MaskRequestBodyCallback != nil && request.Body != nil && !bytes.Equal(request.Body, bodyTooLarge) {
		maskedBody := rl.config.MaskRequestBodyCallback(request)
		if maskedBody == nil {
			request.Body = bodyMasked
		} else {
			request.Body = maskedBody
		}
	}

	// Apply user-provided MaskResponseBodyCallback function
	if rl.config.MaskResponseBodyCallback != nil && response.Body != nil && !bytes.Equal(response.Body, bodyTooLarge) {
		maskedBody := rl.config.MaskResponseBodyCallback(request, response)
		if maskedBody == nil {
			response.Body = bodyMasked
		} else {
			response.Body = maskedBody
		}
	}

	// Check request and response body sizes
	if request.Body != nil && len(request.Body) > common.MaxBodySize {
		request.Body = bodyTooLarge
	}
	if response.Body != nil && len(response.Body) > common.MaxBodySize {
		response.Body = bodyTooLarge
	}

	// Mask request and response body fields
	if request.Body != nil && !bytes.Equal(request.Body, bodyTooLarge) && !bytes.Equal(request.Body, bodyMasked) {
		if rl.hasJSONContentType(request.Headers) {
			request.Body = rl.maskJSONBody(request.Body)
		}
	}
	if response.Body != nil && !bytes.Equal(response.Body, bodyTooLarge) && !bytes.Equal(response.Body, bodyMasked) {
		if rl.hasJSONContentType(response.Headers) {
			response.Body = rl.maskJSONBody(response.Body)
		}
	}

	// Mask request and response headers
	if !rl.config.LogRequestHeaders {
		request.Headers = nil
	} else if request.Headers != nil {
		request.Headers = rl.maskHeaders(request.Headers)
	}
	if !rl.config.LogResponseHeaders {
		response.Headers = nil
	} else if response.Headers != nil {
		response.Headers = rl.maskHeaders(response.Headers)
	}

	// Mask query params
	parsedURL, err := url.Parse(request.URL)
	if err == nil {
		if rl.config.LogQueryParams {
			parsedURL.RawQuery = rl.maskQueryParams(parsedURL.RawQuery)
		} else {
			parsedURL.RawQuery = ""
		}
		request.URL = parsedURL.String()
	}
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
	rl.currentFileMutex.Lock()
	defer rl.currentFileMutex.Unlock()

	if rl.currentFile != nil {
		if err := rl.currentFile.Close(); err != nil {
			return err
		}

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

	for {
		select {
		case <-ticker.C:
			// Write any pending items to the current file
			if err := rl.writeToFile(); err != nil {
				continue
			}

			// Check if the current file is too large and rotate if necessary
			rl.currentFileMutex.Lock()
			shouldRotate := rl.currentFile != nil && rl.currentFile.Size() > maxFileSize
			rl.currentFileMutex.Unlock()

			if shouldRotate {
				if err := rl.rotateFile(); err != nil {
					continue
				}
			}

			// Clean up excess files
			for len(rl.files) > maxFiles {
				file := <-rl.files
				_ = file.Delete()
			}

			// Check if the logger is suspended and resume if necessary
			rl.enabledMutex.Lock()
			if rl.suspendUntil != nil && time.Now().After(*rl.suspendUntil) {
				rl.suspendUntil = nil
			}
			rl.enabledMutex.Unlock()

		case <-rl.done:
			return
		}
	}
}

func (rl *RequestLogger) Clear() error {
	// Drain and delete all pending writes
	for len(rl.pendingWrites) > 0 {
		<-rl.pendingWrites
	}

	// Rotate the file to ensure it's closed
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
	if rl.IsEnabled() {
		rl.enabledMutex.Lock()
		defer rl.enabledMutex.Unlock()

		rl.enabled = false
		if rl.done != nil {
			close(rl.done)
		}
	}
	return rl.Clear()
}

func (rl *RequestLogger) shouldExcludePath(urlPath string) bool {
	patterns := slices.Clone(excludePathPatterns)
	if rl.config.ExcludePaths != nil {
		patterns = append(patterns, rl.config.ExcludePaths...)
	}
	for _, pattern := range patterns {
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
	patterns := slices.Clone(maskQueryParamPatterns)
	if rl.config.MaskQueryParams != nil {
		patterns = append(patterns, rl.config.MaskQueryParams...)
	}
	for _, pattern := range patterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) shouldMaskHeader(name string) bool {
	patterns := slices.Clone(maskHeaderPatterns)
	if rl.config.MaskHeaders != nil {
		patterns = append(patterns, rl.config.MaskHeaders...)
	}
	for _, pattern := range patterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) shouldMaskBodyField(fieldName string) bool {
	patterns := slices.Clone(maskBodyFieldPatterns)
	if rl.config.MaskBodyFields != nil {
		patterns = append(patterns, rl.config.MaskBodyFields...)
	}
	for _, pattern := range patterns {
		if pattern.MatchString(fieldName) {
			return true
		}
	}
	return false
}

func (rl *RequestLogger) hasSupportedContentType(headers [][2]string) bool {
	for _, header := range headers {
		if header[0] == "Content-Type" {
			return rl.IsSupportedContentType(header[1])
		}
	}
	return false
}

func (rl *RequestLogger) hasJSONContentType(headers [][2]string) bool {
	for _, header := range headers {
		if header[0] == "Content-Type" {
			return jsonContentTypePattern.MatchString(header[1])
		}
	}
	return false
}

func (rl *RequestLogger) IsSupportedContentType(contentType string) bool {
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

func (rl *RequestLogger) maskBodyFields(data any) any {
	switch v := data.(type) {
	case map[string]any:
		for key, value := range v {
			if rl.shouldMaskBodyField(key) {
				if _, ok := value.(string); ok {
					v[key] = masked
					continue
				}
			}
			v[key] = rl.maskBodyFields(value)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = rl.maskBodyFields(item)
		}
		return v
	default:
		return v
	}
}

func (rl *RequestLogger) maskJSONBody(body []byte) []byte {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}

	rl.maskBodyFields(data)
	maskedBody, err := json.Marshal(data)
	if err != nil {
		return body
	}

	return maskedBody
}
