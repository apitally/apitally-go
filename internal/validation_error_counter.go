package internal

import (
	"crypto/md5"
	"fmt"
	"strings"
	"sync"
)

// ValidationErrorsItem represents aggregated validation error data
type ValidationErrorsItem struct {
	Consumer   string   `json:"consumer,omitempty"`
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Loc        []string `json:"loc"`
	Msg        string   `json:"msg"`
	Type       string   `json:"type"`
	ErrorCount int      `json:"error_count"`
}

// ValidationErrorCounter tracks and aggregates validation errors
type ValidationErrorCounter struct {
	errorCounts  map[string]int
	errorDetails map[string]ValidationErrorsItem
	mutex        sync.Mutex
}

// NewValidationErrorCounter creates a new ValidationErrorCounter instance
func NewValidationErrorCounter() *ValidationErrorCounter {
	return &ValidationErrorCounter{
		errorCounts:  make(map[string]int),
		errorDetails: make(map[string]ValidationErrorsItem),
	}
}

// AddValidationError adds a validation error to the counter
func (vc *ValidationErrorCounter) AddValidationError(consumer, method, path string, loc, msg, errType string) {
	// Generate key using MD5 hash of error details
	hashInput := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		consumer,
		strings.ToUpper(method),
		path,
		loc,
		strings.TrimSpace(msg),
		errType)

	key := fmt.Sprintf("%x", md5.Sum([]byte(hashInput)))

	vc.mutex.Lock()
	defer vc.mutex.Unlock()

	// Store error details if not already present
	if _, exists := vc.errorDetails[key]; !exists {
		vc.errorDetails[key] = ValidationErrorsItem{
			Consumer: consumer,
			Method:   method,
			Path:     path,
			Loc:      strings.Split(loc, "."),
			Msg:      msg,
			Type:     errType,
		}
	}

	// Increment error count
	vc.errorCounts[key]++
}

// GetAndResetValidationErrors returns the current validation error data and resets all counters
func (vc *ValidationErrorCounter) GetAndResetValidationErrors() []ValidationErrorsItem {
	vc.mutex.Lock()
	defer vc.mutex.Unlock()

	data := make([]ValidationErrorsItem, 0, len(vc.errorCounts))

	for key, count := range vc.errorCounts {
		if details, exists := vc.errorDetails[key]; exists {
			item := details
			item.ErrorCount = count
			data = append(data, item)
		}
	}

	// Reset all maps
	vc.errorCounts = make(map[string]int)
	vc.errorDetails = make(map[string]ValidationErrorsItem)

	return data
}
