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

func getLoggedItems(t *testing.T, requestLogger *RequestLogger) []map[string]any {
	requestLogger.writeToFile()
	requestLogger.rotateFile()

	logFile := requestLogger.GetFile()
	if logFile == nil {
		return []map[string]any{}
	}

	content, err := logFile.GetContent()
	assert.NoError(t, err)

	// Extract and decompress the content
	lineReader, err := gzip.NewReader(bytes.NewReader(content))
	assert.NoError(t, err)
	defer lineReader.Close()

	scanner := bufio.NewScanner(lineReader)
	var items []map[string]any
	for scanner.Scan() {
		var logItem map[string]any
		err := json.Unmarshal(scanner.Bytes(), &logItem)
		assert.NoError(t, err)
		items = append(items, logItem)
	}

	logFile.Delete()
	return items
}

func TestRequestLogger(t *testing.T) {
	t.Run("LogRequest", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.LogRequestHeaders = true
		config.LogRequestBody = true
		config.LogResponseBody = true
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		now := time.Now()
		timestamp := float64(now.UnixMilli()) / 1000.0
		startTimeNs := now.UnixNano()
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
		spans := []SpanData{
			{
				SpanID:    "a1b2c3d4e5f67890",
				Name:      "GET /items",
				Kind:      "internal",
				StartTime: startTimeNs,
				EndTime:   startTimeNs + 100*int64(time.Millisecond),
			},
			{
				SpanID:       "1234567890abcdef",
				ParentSpanID: "a1b2c3d4e5f67890",
				Name:         "db.query",
				Kind:         "client",
				StartTime:    startTimeNs + 10*int64(time.Millisecond),
				EndTime:      startTimeNs + 50*int64(time.Millisecond),
			},
		}
		traceID := "0123456789abcdef0123456789abcdef"
		requestLogger.LogRequest(request, response, errors.New("test"), "", spans, traceID)

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)

		reqData := items[0]["request"].(map[string]any)
		assert.Equal(t, "GET", reqData["method"])
		assert.Equal(t, "/items", reqData["path"])
		assert.Equal(t, "http://test/items", reqData["url"])

		respData := items[0]["response"].(map[string]any)
		assert.Equal(t, float64(200), respData["status_code"])
		assert.Equal(t, 0.123, respData["response_time"])

		responseBody, err := base64.StdEncoding.DecodeString(respData["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, `{"items":[]}`, string(responseBody))

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

		exceptionData := items[0]["exception"].(map[string]any)
		assert.Equal(t, "errors.errorString", exceptionData["type"])
		assert.Equal(t, "test", exceptionData["message"])

		// Check trace ID and spans
		assert.Equal(t, "0123456789abcdef0123456789abcdef", items[0]["trace_id"])
		spansData := items[0]["spans"].([]any)
		assert.Len(t, spansData, 2)
		span0 := spansData[0].(map[string]any)
		assert.Equal(t, "a1b2c3d4e5f67890", span0["span_id"])
		assert.Equal(t, "GET /items", span0["name"])
		span1 := spansData[1].(map[string]any)
		assert.Equal(t, "1234567890abcdef", span1["span_id"])
		assert.Equal(t, "a1b2c3d4e5f67890", span1["parent_span_id"])
		assert.Equal(t, "db.query", span1["name"])

		// Cleanup
		requestLogger.Clear()
		assert.Nil(t, requestLogger.GetFile())
	})

	t.Run("ExcludeBasedOnConfig", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.LogQueryParams = false
		config.LogResponseHeaders = false
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
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)

		reqData := items[0]["request"].(map[string]any)
		assert.Equal(t, "http://test/items", reqData["url"])
		assert.Nil(t, reqData["headers"])
		assert.Nil(t, reqData["body"])

		respData := items[0]["response"].(map[string]any)
		assert.Nil(t, respData["headers"])
		assert.Nil(t, respData["body"])
	})

	t.Run("ExcludeUsingCallback", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.ExcludeCallback = func(req *common.Request, resp *common.Response) bool {
			return strings.Contains(req.Consumer, "tester")
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
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 0)
	})

	t.Run("ExcludeBasedOnPath", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.ExcludePaths = []*regexp.Regexp{regexp.MustCompile(`/status$`)}
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
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		request = &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/status",
			URL:       "http://test/status",
			Headers:   [][2]string{},
			Body:      []byte{},
		}
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 0)
	})

	t.Run("ExcludeHealthCheckUserAgent", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
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
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 0)
	})

	t.Run("MaskHeaders", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.LogRequestHeaders = true
		config.MaskHeaders = []*regexp.Regexp{regexp.MustCompile(`(?i)test`)}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/test",
			URL:       "http://localhost:8000/test?foo=bar",
			Headers: [][2]string{
				{"Accept", "text/plain"},
				{"Content-Type", "text/plain"},
				{"Authorization", "Bearer 123456"},
				{"X-Test", "123456"},
			},
			Body: []byte("test"),
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.1,
			Headers:      [][2]string{{"Content-Type", "text/plain"}},
			Body:         []byte("test"),
		}
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)
		reqData := items[0]["request"].(map[string]any)
		reqHeaders := reqData["headers"].([]any)

		authMasked := false
		testMasked := false
		acceptNotMasked := false
		for _, h := range reqHeaders {
			header := h.([]any)
			if header[0] == "Authorization" && header[1] == "******" {
				authMasked = true
			}
			if header[0] == "X-Test" && header[1] == "******" {
				testMasked = true
			}
			if header[0] == "Accept" && header[1] == "text/plain" {
				acceptNotMasked = true
			}
		}

		assert.True(t, authMasked, "Authorization header should be masked")
		assert.True(t, testMasked, "X-Test header should be masked")
		assert.True(t, acceptNotMasked, "Accept header should not be masked")
	})

	t.Run("MaskQueryParams", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.MaskQueryParams = []*regexp.Regexp{regexp.MustCompile(`(?i)test`)}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/test",
			URL:       "http://localhost/test?secret=123456&test=123456&other=abcdef",
			Headers:   [][2]string{{"Accept", "text/plain"}},
			Body:      []byte("test"),
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.1,
			Headers:      [][2]string{{"Content-Type", "text/plain"}},
			Body:         []byte("test"),
		}
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)
		reqData := items[0]["request"].(map[string]any)
		url := reqData["url"].(string)

		// Check that secret and test query params are masked but other is not
		assert.Contains(t, url, "secret=%2A%2A%2A%2A%2A%2A")
		assert.Contains(t, url, "test=%2A%2A%2A%2A%2A%2A")
		assert.Contains(t, url, "other=abcdef")
	})

	t.Run("MaskBodyCallbacks", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.LogRequestBody = true
		config.LogResponseBody = true
		config.MaskRequestBodyCallback = func(req *common.Request) []byte {
			if req.Method == "GET" && req.Path == "/test" {
				return nil
			}
			return req.Body
		}
		config.MaskResponseBodyCallback = func(req *common.Request, resp *common.Response) []byte {
			if req.Method == "GET" && req.Path == "/test" {
				return nil
			}
			return resp.Body
		}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/test",
			URL:       "http://localhost:8000/test?foo=bar",
			Headers:   [][2]string{{"Content-Type", "application/json"}},
			Body:      []byte("test"),
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.1,
			Headers:      [][2]string{{"Content-Type", "application/json"}},
			Body:         []byte("test"),
		}
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)

		reqBody, err := base64.StdEncoding.DecodeString(items[0]["request"].(map[string]any)["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, "<masked>", string(reqBody))

		respBody, err := base64.StdEncoding.DecodeString(items[0]["response"].(map[string]any)["body"].(string))
		assert.NoError(t, err)
		assert.Equal(t, "<masked>", string(respBody))
	})

	t.Run("MaskBodyFields", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		config.LogRequestBody = true
		config.LogResponseBody = true
		config.MaskBodyFields = []*regexp.Regexp{regexp.MustCompile(`(?i)custom`)}
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		requestBody := map[string]any{
			"username":     "john_doe",
			"password":     "secret123",
			"token":        "abc123",
			"custom":       "xyz789",
			"user_id":      42,
			"api_key":      123,
			"normal_field": "value",
			"nested": map[string]any{
				"password": "nested_secret",
				"count":    5,
				"deeper": map[string]any{
					"auth": "deep_token",
				},
			},
			"array": []any{
				map[string]any{"password": "array_secret", "id": 1},
				map[string]any{"normal": "text", "token": "array_token"},
			},
		}
		responseBody := map[string]any{
			"status": "success",
			"secret": "response_secret",
			"data":   map[string]any{"pwd": "response_pwd"},
		}

		requestBodyJSON, _ := json.Marshal(requestBody)
		responseBodyJSON, _ := json.Marshal(responseBody)

		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "POST",
			Path:      "/test",
			URL:       "http://localhost:8000/test?foo=bar",
			Headers:   [][2]string{{"Content-Type", "application/json"}},
			Body:      requestBodyJSON,
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.1,
			Headers:      [][2]string{{"Content-Type", "application/json"}},
			Body:         responseBodyJSON,
		}
		requestLogger.LogRequest(request, response, nil, "", nil, "")

		items := getLoggedItems(t, requestLogger)
		assert.Len(t, items, 1)

		reqBodyDecoded, err := base64.StdEncoding.DecodeString(items[0]["request"].(map[string]any)["body"].(string))
		assert.NoError(t, err)
		var maskedRequestBody map[string]any
		err = json.Unmarshal(reqBodyDecoded, &maskedRequestBody)
		assert.NoError(t, err)

		respBodyDecoded, err := base64.StdEncoding.DecodeString(items[0]["response"].(map[string]any)["body"].(string))
		assert.NoError(t, err)
		var maskedResponseBody map[string]any
		err = json.Unmarshal(respBodyDecoded, &maskedResponseBody)
		assert.NoError(t, err)

		// Test fields that should be masked
		assert.Equal(t, "******", maskedRequestBody["password"])
		assert.Equal(t, "******", maskedRequestBody["token"])
		assert.Equal(t, "******", maskedRequestBody["custom"])
		assert.Equal(t, "******", maskedRequestBody["nested"].(map[string]any)["password"])
		assert.Equal(t, "******", maskedRequestBody["nested"].(map[string]any)["deeper"].(map[string]any)["auth"])
		assert.Equal(t, "******", maskedRequestBody["array"].([]any)[0].(map[string]any)["password"])
		assert.Equal(t, "******", maskedRequestBody["array"].([]any)[1].(map[string]any)["token"])
		assert.Equal(t, "******", maskedResponseBody["secret"])
		assert.Equal(t, "******", maskedResponseBody["data"].(map[string]any)["pwd"])

		// Test fields that should NOT be masked
		assert.Equal(t, "john_doe", maskedRequestBody["username"])
		assert.Equal(t, float64(42), maskedRequestBody["user_id"])
		assert.Equal(t, float64(123), maskedRequestBody["api_key"])
		assert.Equal(t, "value", maskedRequestBody["normal_field"])
		assert.Equal(t, float64(5), maskedRequestBody["nested"].(map[string]any)["count"])
		assert.Equal(t, float64(1), maskedRequestBody["array"].([]any)[0].(map[string]any)["id"])
		assert.Equal(t, "text", maskedRequestBody["array"].([]any)[1].(map[string]any)["normal"])
		assert.Equal(t, "success", maskedResponseBody["status"])
	})

	t.Run("Suspend", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
		requestLogger := NewRequestLogger(config)
		defer requestLogger.Close()

		requestLogger.SuspendFor(1 * time.Second)
		assert.True(t, requestLogger.IsSuspended())
	})

	t.Run("RetryFileLater", func(t *testing.T) {
		config := common.NewRequestLoggingConfig()
		config.Enabled = true
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
		requestLogger := NewRequestLogger(common.NewRequestLoggingConfig())
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
