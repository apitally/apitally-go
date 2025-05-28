package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResponseWriter(t *testing.T) {
	t.Run("CaptureBody", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := &bytes.Buffer{}
		rw := &ResponseWriter{
			ResponseWriter: recorder,
			Body:           body,
			IsSupportedContentType: func(contentType string) bool {
				return contentType == "application/json"
			},
		}

		// Test default status
		assert.Equal(t, http.StatusOK, rw.Status())
		assert.Equal(t, int64(0), rw.Size())

		// Test status code setting
		rw.WriteHeader(http.StatusCreated)
		assert.Equal(t, http.StatusCreated, rw.Status())

		// Test body capture for supported content type
		rw.Header().Set("Content-Type", "application/json")
		rw.Write([]byte("test"))
		assert.Equal(t, "test", body.String())
		assert.Equal(t, int64(4), rw.Size())
	})

	t.Run("UnsupportedContentType", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := &bytes.Buffer{}
		rw := &ResponseWriter{
			ResponseWriter: recorder,
			Body:           body,
			IsSupportedContentType: func(contentType string) bool {
				return contentType == "application/json"
			},
		}

		rw.Header().Set("Content-Type", "text/plain")
		rw.Write([]byte("test"))
		assert.Empty(t, body.String())
		assert.Equal(t, int64(4), rw.Size())
	})

	t.Run("MaxBodySizeExceeded", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		body := &bytes.Buffer{}
		rw := &ResponseWriter{
			ResponseWriter: recorder,
			Body:           body,
			IsSupportedContentType: func(contentType string) bool {
				return true
			},
		}

		rw.Header().Set("Content-Type", "application/json")

		// Write data that exceeds MaxBodySize
		largeData := make([]byte, MaxBodySize+1)
		for i := range largeData {
			largeData[i] = 'a'
		}

		rw.Write(largeData)
		assert.Empty(t, body.String()) // Body should be reset when max size exceeded
		assert.Equal(t, int64(MaxBodySize+1), rw.Size())
	})
}
