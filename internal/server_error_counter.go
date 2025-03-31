package internal

import (
	"crypto/md5"
	"fmt"
	"strings"
)

const (
	maxMsgLength        = 2048
	maxStacktraceLength = 65536
)

// ServerErrorsItem represents aggregated server error data
type ServerErrorsItem struct {
	Consumer      string  `json:"consumer,omitempty"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	Type          string  `json:"type"`
	Msg           string  `json:"msg"`
	Traceback     string  `json:"traceback"`
	SentryEventID *string `json:"sentry_event_id"`
	ErrorCount    int     `json:"error_count"`
}

// ServerErrorCounter tracks and aggregates server errors
type ServerErrorCounter struct {
	errorCounts    map[string]int
	errorDetails   map[string]ServerErrorsItem
	sentryEventIDs map[string]string
}

// NewServerErrorCounter creates a new ServerErrorCounter instance
func NewServerErrorCounter() *ServerErrorCounter {
	return &ServerErrorCounter{
		errorCounts:    make(map[string]int),
		errorDetails:   make(map[string]ServerErrorsItem),
		sentryEventIDs: make(map[string]string),
	}
}

// AddServerError adds a server error to the counter
func (sc *ServerErrorCounter) AddServerError(consumer, method, path, errType, msg, traceback string, sentryEventID *string) {
	// Generate key using MD5 hash of error details
	hashInput := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		consumer,
		strings.ToUpper(method),
		path,
		errType,
		strings.TrimSpace(msg),
		strings.TrimSpace(traceback))

	key := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	// Store error details if not already present
	if _, exists := sc.errorDetails[key]; !exists {
		sc.errorDetails[key] = ServerErrorsItem{
			Consumer:  consumer,
			Method:    method,
			Path:      path,
			Type:      errType,
			Msg:       truncateExceptionMessage(msg),
			Traceback: truncateExceptionStackTrace(traceback),
		}
	}

	// Increment error count
	sc.errorCounts[key]++

	// Store Sentry event ID if present
	if sentryEventID != nil {
		sc.sentryEventIDs[key] = *sentryEventID
	}
}

// GetAndResetServerErrors returns the current server error data and resets all counters
func (sc *ServerErrorCounter) GetAndResetServerErrors() []ServerErrorsItem {
	data := make([]ServerErrorsItem, 0, len(sc.errorCounts))

	for key, count := range sc.errorCounts {
		if details, exists := sc.errorDetails[key]; exists {
			var sentryEventID *string
			if id, hasID := sc.sentryEventIDs[key]; hasID {
				sentryEventID = &id
			}

			item := details
			item.ErrorCount = count
			item.SentryEventID = sentryEventID
			data = append(data, item)
		}
	}

	// Reset all maps
	sc.errorCounts = make(map[string]int)
	sc.errorDetails = make(map[string]ServerErrorsItem)
	sc.sentryEventIDs = make(map[string]string)

	return data
}

// Helper functions

func truncateExceptionMessage(msg string) string {
	if len(msg) <= maxMsgLength {
		return msg
	}
	suffix := "... (truncated)"
	cutoff := maxMsgLength - len(suffix)
	return msg[:cutoff] + suffix
}

func truncateExceptionStackTrace(stack string) string {
	suffix := "... (truncated) ..."
	cutoff := maxStacktraceLength - len(suffix)
	lines := strings.Split(strings.TrimSpace(stack), "\n")
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
