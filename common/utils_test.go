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

	t.Run("isHTTPS", func(t *testing.T) {
		check := func(header, value string) bool {
			h := http.Header{}
			h.Set(header, value)
			return isHTTPS(h)
		}
		assert.True(t, check("X-Forwarded-Proto", "https"))
		assert.True(t, check("X-Forwarded-Proto", "https, http"))
		assert.False(t, check("X-Forwarded-Proto", "http"))
		assert.True(t, check("X-Forwarded-Protocol", "https"))
		assert.True(t, check("X-Forwarded-Scheme", "https"))
		assert.True(t, check("X-Url-Scheme", "https"))
		assert.True(t, check("X-Scheme", "https"))
		assert.True(t, check("Forwarded", "for=192.0.2.1;proto=https;host=example.com"))
		assert.True(t, check("Forwarded", "proto=\"https\""))
		assert.False(t, check("Forwarded", "for=192.0.2.1;proto=http"))
		assert.True(t, check("Front-End-Https", "on"))
		assert.True(t, check("X-Forwarded-Ssl", "on"))
		assert.False(t, isHTTPS(http.Header{}))
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
