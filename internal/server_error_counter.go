package internal

import (
	"crypto/md5"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const (
	maxMsgLength        = 2048
	maxStacktraceLength = 65536
)

var hexAddressRegex = regexp.MustCompile(`0x[0-9a-fA-F]+`)
var goRoutineRegex = regexp.MustCompile(`goroutine \d+`)

// ServerErrorsItem represents aggregated server error data
type ServerErrorsItem struct {
	Consumer      string  `json:"consumer,omitempty"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	Type          string  `json:"type"`
	Message       string  `json:"msg"`
	StackTrace    string  `json:"traceback"`
	SentryEventID *string `json:"sentry_event_id"`
	ErrorCount    int     `json:"error_count"`
}

// ServerErrorCounter tracks and aggregates server errors
type ServerErrorCounter struct {
	errorCounts  map[string]int
	errorDetails map[string]ServerErrorsItem
	mutex        sync.Mutex
}

// NewServerErrorCounter creates a new ServerErrorCounter instance
func NewServerErrorCounter() *ServerErrorCounter {
	return &ServerErrorCounter{
		errorCounts:  make(map[string]int),
		errorDetails: make(map[string]ServerErrorsItem),
	}
}

// AddServerError adds a server error to the counter
func (sc *ServerErrorCounter) AddServerError(consumer, method, path string, handlerError error, stackTrace string) {
	errorType := getErrorType(handlerError)
	errorMessage := handlerError.Error()

	// Generate key using MD5 hash of error details
	hashInput := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		consumer,
		strings.ToUpper(method),
		path,
		errorType,
		errorMessage,
		stripStackTraceForHashing(stackTrace))

	key := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	// Store error details if not already present
	if _, exists := sc.errorDetails[key]; !exists {
		sc.errorDetails[key] = ServerErrorsItem{
			Consumer:   consumer,
			Method:     method,
			Path:       path,
			Type:       errorType,
			Message:    truncateExceptionMessage(errorMessage),
			StackTrace: truncateExceptionStackTrace(stackTrace),
		}
	}

	// Increment error count
	sc.errorCounts[key]++
}

// GetAndResetServerErrors returns the current server error data and resets all counters
func (sc *ServerErrorCounter) GetAndResetServerErrors() []ServerErrorsItem {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	data := make([]ServerErrorsItem, 0, len(sc.errorCounts))

	for key, count := range sc.errorCounts {
		if details, exists := sc.errorDetails[key]; exists {
			item := details
			item.ErrorCount = count
			data = append(data, item)
		}
	}

	// Reset all maps
	sc.errorCounts = make(map[string]int)
	sc.errorDetails = make(map[string]ServerErrorsItem)

	return data
}

// Helper functions

func getErrorType(err error) string {
	errorType := reflect.TypeOf(err)
	if errorType.Kind() == reflect.Ptr {
		errorType = errorType.Elem()
	}
	return errorType.String()
}

func truncateExceptionMessage(msg string) string {
	if len(msg) <= maxMsgLength {
		return msg
	}
	suffix := "... (truncated)"
	cutoff := maxMsgLength - len(suffix)
	return msg[:cutoff] + suffix
}

func truncateExceptionStackTrace(stackTrace string) string {
	suffix := "... (truncated) ..."
	cutoff := maxStacktraceLength - len(suffix)
	lines := strings.Split(strings.TrimSpace(stackTrace), "\n")
	if len(lines) > 5 {
		// Remove lines related to ApitallyMiddleware recovering and re-panicking
		lines = slices.Delete(lines, 1, 5)
	}
	var truncatedLines []string
	length := 0

	for _, line := range lines {
		if length+len(line)+1 > cutoff {
			truncatedLines = append(truncatedLines, suffix)
			break
		}
		truncatedLines = append(truncatedLines, line)
		length += len(line) + 1
	}

	return strings.Join(truncatedLines, "\n")
}

func stripStackTraceForHashing(stackTrace string) string {
	stackTrace = hexAddressRegex.ReplaceAllString(stackTrace, "0x0")
	stackTrace = goRoutineRegex.ReplaceAllString(stackTrace, "goroutine 0")
	return stackTrace
}
