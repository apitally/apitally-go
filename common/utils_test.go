package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUtils(t *testing.T) {
	t.Run("GetFullURL", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?q=1", nil)
		req.Host = "example.com"
		assert.Equal(t, "http://example.com/test?q=1", GetFullURL(req))

		req.Header.Set("X-Forwarded-Proto", "https")
		assert.Equal(t, "https://example.com/test?q=1", GetFullURL(req))
	})

	t.Run("ParseContentLength", func(t *testing.T) {
		assert.Equal(t, int64(-1), ParseContentLength(""))
		assert.Equal(t, int64(-1), ParseContentLength("invalid"))
		assert.Equal(t, int64(123), ParseContentLength("123"))
	})

	t.Run("TransformHeaders", func(t *testing.T) {
		header := http.Header{}
		header.Add("Content-Type", "application/json")
		header.Add("Accept", "application/json")
		header.Add("Accept", "text/plain")

		headers := TransformHeaders(header)
		assert.Equal(t, 3, len(headers))
		assert.Contains(t, headers, [2]string{"Content-Type", "application/json"})
		assert.Contains(t, headers, [2]string{"Accept", "application/json"})
		assert.Contains(t, headers, [2]string{"Accept", "text/plain"})
	})

	t.Run("TruncateValidationErrorMessage", func(t *testing.T) {
		msg := "Key: 'User.Name' Error: required field"
		assert.Equal(t, "required field", TruncateValidationErrorMessage(msg))

		msg = "some other error"
		assert.Equal(t, msg, TruncateValidationErrorMessage(msg))
	})
}
