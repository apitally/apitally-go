package internal

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServerErrorCounter(t *testing.T) {
	t.Run("Truncation", func(t *testing.T) {
		serverErrorCounter := NewServerErrorCounter()

		// Create a long error message
		errorMsg := strings.Repeat("a", 3000)
		err := errors.New(errorMsg)

		// Create a long stacktrace
		stacktrace := strings.Repeat("one line\n", 10000)

		// Add server error to counter
		serverErrorCounter.AddServerError("test", "GET", "/test", err, stacktrace)

		// Get and reset server errors
		serverErrors := serverErrorCounter.GetAndResetServerErrors()

		// Assert message and stacktrace are truncated
		assert.Len(t, serverErrors, 1)
		assert.Equal(t, 2048, len(serverErrors[0].Message))
		assert.Contains(t, serverErrors[0].Message, "(truncated)")
		assert.Less(t, len(serverErrors[0].StackTrace), 65536)
		assert.Contains(t, serverErrors[0].StackTrace, "(truncated)")
	})

	t.Run("Aggregation", func(t *testing.T) {
		serverErrorCounter := NewServerErrorCounter()

		// Create error and stacktrace
		err1 := errors.New("test error 1")
		stacktrace := "test stacktrace"

		// Add the same error multiple times
		for i := 0; i < 3; i++ {
			serverErrorCounter.AddServerError("test", "GET", "/test", err1, stacktrace)
		}

		// Add a different error
		err2 := errors.New("test error 2")
		serverErrorCounter.AddServerError("test", "POST", "/test", err2, stacktrace)

		// Get and reset server errors
		serverErrors := serverErrorCounter.GetAndResetServerErrors()

		// Assert that we have two error entries
		assert.Len(t, serverErrors, 2)

		// Create a map of error messages to their counts
		errorCounts := make(map[string]int)
		for _, e := range serverErrors {
			errorCounts[e.Message] = e.ErrorCount
		}

		// Assert counts are correct
		assert.Equal(t, 3, errorCounts["test error 1"])
		assert.Equal(t, 1, errorCounts["test error 2"])
	})
}
