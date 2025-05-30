package internal

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/stretchr/testify/assert"
)

func TestRequestLogger(t *testing.T) {
	t.Run("LogRequest", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled:            true,
			LogQueryParams:     true,
			LogRequestHeaders:  true,
			LogRequestBody:     true,
			LogResponseHeaders: true,
			LogResponseBody:    true,
			LogPanic:           true,
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Consumer:  "tester",
			Method:    "GET",
			Path:      "/items",
			URL:       "http://test/items",
			Headers:   [][2]string{{"User-Agent", "Test"}},
			Body:      []byte{},
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{{"Content-Type", "application/json"}},
			Size:         13,
			Body:         []byte(`{"items": []}`),
		}
		requestLogger.LogRequest(request, response, errors.New("test"), "")
		requestLogger.writeToFile()
		requestLogger.rotateFile()

		logFile := requestLogger.GetFile()
		assert.NotNil(t, logFile)
		assert.True(t, logFile.Size() > 0)

		content, err := logFile.GetContent()
		assert.NoError(t, err)

		// Extract and decompress the content
		lineReader, err := gzip.NewReader(bytes.NewReader(content))
		assert.NoError(t, err)

		scanner := bufio.NewScanner(lineReader)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		assert.Len(t, lines, 1)

		var logItem map[string]any
		err = json.Unmarshal([]byte(lines[0]), &logItem)
		assert.NoError(t, err)

		reqData := logItem["request"].(map[string]any)
		assert.Equal(t, "GET", reqData["method"])
		assert.Equal(t, "/items", reqData["path"])
		assert.Equal(t, "http://test/items", reqData["url"])

		respData := logItem["response"].(map[string]any)
		assert.Equal(t, float64(200), respData["status_code"])
		assert.Equal(t, 0.123, respData["response_time"])

		responseBody, err := base64.StdEncoding.DecodeString(respData["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, `{"items": []}`, string(responseBody))

		reqHeaders := reqData["headers"].([]any)
		assert.Len(t, reqHeaders, 1)
		header := reqHeaders[0].([]any)
		assert.Equal(t, "User-Agent", header[0])
		assert.Equal(t, "Test", header[1])

		respHeaders := respData["headers"].([]any)
		assert.Len(t, respHeaders, 1)
		header = respHeaders[0].([]any)
		assert.Equal(t, "Content-Type", header[0])
		assert.Equal(t, "application/json", header[1])

		exceptionData := logItem["exception"].(map[string]any)
		assert.Equal(t, "errors.errorString", exceptionData["type"])
		assert.Equal(t, "test", exceptionData["message"])

		// Cleanup
		requestLogger.Clear()
		assert.Nil(t, requestLogger.GetFile())
	})

	t.Run("ExcludeBasedOnConfig", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled:            true,
			LogQueryParams:     false,
			LogRequestHeaders:  false,
			LogRequestBody:     false,
			LogResponseHeaders: false,
			LogResponseBody:    false,
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "POST",
			Path:      "/items",
			URL:       "http://test/items?token=my-secret-token",
			Headers: [][2]string{
				{"Authorization", "Bearer 1234567890"},
				{"Content-Type", "application/json"},
			},
			Body: []byte(`{"key": "value"}`),
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{{"Content-Type", "application/json"}},
			Body:         []byte(`{"key": "value"}`),
		}

		requestLogger.LogRequest(request, response, nil, "")
		requestLogger.writeToFile()
		requestLogger.rotateFile()

		logFile := requestLogger.GetFile()
		assert.NotNil(t, logFile)
		assert.True(t, logFile.Size() > 0)

		content, err := logFile.GetContent()
		assert.NoError(t, err)

		// Extract and decompress the content
		lineReader, err := gzip.NewReader(bytes.NewReader(content))
		assert.NoError(t, err)

		scanner := bufio.NewScanner(lineReader)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		assert.Len(t, lines, 1)

		var logItem map[string]any
		err = json.Unmarshal([]byte(lines[0]), &logItem)
		assert.NoError(t, err)

		reqData := logItem["request"].(map[string]any)
		assert.Equal(t, "http://test/items", reqData["url"])
		assert.Nil(t, reqData["headers"])
		assert.Nil(t, reqData["body"])

		respData := logItem["response"].(map[string]any)
		assert.Nil(t, respData["headers"])
		assert.Nil(t, respData["body"])
	})

	t.Run("ExcludeUsingCallback", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled: true,
			ExcludeCallback: func(req *common.Request, resp *common.Response) bool {
				return strings.Contains(req.Consumer, "tester")
			},
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Consumer:  "tester",
			Method:    "GET",
			Path:      "/items",
			URL:       "http://test/items",
			Headers:   [][2]string{},
			Body:      []byte{},
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{},
			Body:         []byte(`{"items": []}`),
		}
		requestLogger.LogRequest(request, response, nil, "")
		requestLogger.writeToFile()
		requestLogger.rotateFile()

		// No log file should be created
		logFile := requestLogger.GetFile()
		assert.Nil(t, logFile)
	})

	t.Run("ExcludeBasedOnPath", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled:      true,
			ExcludePaths: []*regexp.Regexp{regexp.MustCompile(`/status$`)},
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/healthz",
			URL:       "http://test/healthz",
			Headers:   [][2]string{},
			Body:      []byte{},
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{},
			Body:         []byte(`{"healthy": true}`),
		}
		requestLogger.LogRequest(request, response, nil, "")

		request = &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/status",
			URL:       "http://test/status",
			Headers:   [][2]string{},
			Body:      []byte{},
		}
		requestLogger.LogRequest(request, response, nil, "")

		requestLogger.writeToFile()
		requestLogger.rotateFile()

		// No log file should be created
		logFile := requestLogger.GetFile()
		assert.Nil(t, logFile)
	})

	t.Run("ExcludeHealthCheckUserAgent", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled: true,
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/",
			URL:       "http://test/",
			Headers:   [][2]string{{"User-Agent", "ELB-HealthChecker/2.0"}},
			Body:      []byte{},
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0,
			Headers:      [][2]string{},
			Body:         []byte{},
		}
		requestLogger.LogRequest(request, response, nil, "")
		requestLogger.writeToFile()
		requestLogger.rotateFile()

		// No log file should be created
		logFile := requestLogger.GetFile()
		assert.Nil(t, logFile)
	})

	t.Run("Masking", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled:            true,
			LogQueryParams:     true,
			LogRequestHeaders:  true,
			LogRequestBody:     true,
			LogResponseHeaders: true,
			LogResponseBody:    true,
			MaskQueryParams:    []*regexp.Regexp{regexp.MustCompile(`(?i)mask`)},
			MaskHeaders:        []*regexp.Regexp{regexp.MustCompile(`(?i)mask`)},
			MaskRequestBodyCallback: func(req *common.Request) []byte {
				return []byte("<masked>")
			},
			MaskResponseBodyCallback: func(req *common.Request, resp *common.Response) []byte {
				return []byte("<masked>")
			},
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "POST",
			Path:      "/items",
			URL:       "http://test/items?token=my-secret-token&mask=123&test=123",
			Headers: [][2]string{
				{"Authorization", "Bearer 1234567890"},
				{"Content-Type", "application/json"},
				{"Mask", "123"},
			},
			Body: []byte(`{"key": "value"}`),
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{{"Content-Type", "application/json"}},
			Body:         []byte(`{"key": "value"}`),
		}
		requestLogger.LogRequest(request, response, nil, "")
		requestLogger.writeToFile()
		requestLogger.rotateFile()

		logFile := requestLogger.GetFile()
		assert.NotNil(t, logFile)
		assert.True(t, logFile.Size() > 0)

		content, err := logFile.GetContent()
		assert.NoError(t, err)

		// Extract and decompress the content
		lineReader, err := gzip.NewReader(bytes.NewReader(content))
		assert.NoError(t, err)

		scanner := bufio.NewScanner(lineReader)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		assert.Len(t, lines, 1)

		var logItem map[string]any
		err = json.Unmarshal([]byte(lines[0]), &logItem)
		assert.NoError(t, err)

		reqData := logItem["request"].(map[string]any)
		assert.Equal(t, "http://test/items?mask=%2A%2A%2A%2A%2A%2A&test=123&token=%2A%2A%2A%2A%2A%2A", reqData["url"])

		reqBody, err := base64.StdEncoding.DecodeString(reqData["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, "<masked>", string(reqBody))

		respBody, err := base64.StdEncoding.DecodeString(logItem["response"].(map[string]any)["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, "<masked>", string(respBody))

		reqHeaders := reqData["headers"].([]any)
		var authHeader string
		var maskHeader string
		for _, h := range reqHeaders {
			header := h.([]any)
			if header[0] == "Authorization" {
				authHeader = header[1].(string)
			}
			if header[0] == "Mask" {
				maskHeader = header[1].(string)
			}
		}
		assert.Equal(t, "******", authHeader)
		assert.Equal(t, "******", maskHeader)
	})

	t.Run("Suspend", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled: true,
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		requestLogger.SuspendFor(1 * time.Second)
		assert.True(t, requestLogger.IsSuspended())
	})

	t.Run("RetryFileLater", func(t *testing.T) {
		config := &common.RequestLoggingConfig{
			Enabled: true,
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		tempFile, _ := NewTempGzipFile()
		tempFile.WriteLine([]byte("test"))
		tempFile.Close()

		requestLogger.RetryFileLater(tempFile)

		// File should be available in the channel
		retrievedFile := requestLogger.GetFile()
		assert.NotNil(t, retrievedFile)
		assert.Equal(t, tempFile, retrievedFile)
		retrievedFile.Delete()

		// Fill the channel to capacity (maxFiles = 50)
		for i := 0; i < 50; i++ {
			file, err := NewTempGzipFile()
			assert.NoError(t, err)
			err = file.Close()
			assert.NoError(t, err)
			requestLogger.RetryFileLater(file)
		}

		// Create another file to retry when channel is full
		tempFile, _ = NewTempGzipFile()
		tempFile.WriteLine([]byte("test"))
		tempFile.Close()

		// This should delete the file since channel is full
		requestLogger.RetryFileLater(tempFile)

		// Verify the overflow file was deleted
		_, err := tempFile.GetContent()
		assert.Error(t, err) // Should error because file was deleted

		// Clean up
		requestLogger.Clear()
	})

	t.Run("IsSupportedContentType", func(t *testing.T) {
		requestLogger := NewRequestLogger(&common.RequestLoggingConfig{})
		defer requestLogger.Close()

		// Supported content types
		assert.True(t, requestLogger.IsSupportedContentType("application/json"))
		assert.True(t, requestLogger.IsSupportedContentType("application/json; charset=utf-8"))
		assert.True(t, requestLogger.IsSupportedContentType("text/plain"))

		// Unsupported content types
		assert.False(t, requestLogger.IsSupportedContentType("multipart/form-data"))
		assert.False(t, requestLogger.IsSupportedContentType(""))
	})
}
