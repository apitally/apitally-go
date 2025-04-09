package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationErrorCounter(t *testing.T) {
	t.Run("Aggregation", func(t *testing.T) {
		validationErrorCounter := NewValidationErrorCounter()

		// Add validation errors
		validationErrorCounter.AddValidationError("test", "GET", "/test", "struct.param", "error message", "")
		validationErrorCounter.AddValidationError("test", "GET", "/test", "struct.param", "error message", "")

		// Get and reset validation errors
		validationErrors := validationErrorCounter.GetAndResetValidationErrors()

		// Assert that we have one validation error
		assert.Len(t, validationErrors, 1)

		// Assert that the validation error count is correct
		assert.Equal(t, 2, validationErrors[0].ErrorCount)
		assert.Equal(t, []string{"struct", "param"}, validationErrors[0].Loc)
		assert.Equal(t, "error message", validationErrors[0].Msg)
	})
}
