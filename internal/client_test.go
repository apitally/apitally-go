package internal

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
)

func TestApitallyClient(t *testing.T) {
	t.Run("StartupSyncShutdown", func(t *testing.T) {
		config := &common.Config{
			ClientId: "e117eb33-f6d2-4260-a71d-31eb49425893",
			Env:      "test",
			RequestLoggingConfig: &common.RequestLoggingConfig{
				Enabled: true,
			},
		}
		httpClient, mockTransport := createMockHTTPClient()
		client, _ := InitApitallyClientWithHTTPClient(*config, httpClient)
		client.StartSync()
		defer client.Shutdown()

		// Set startup data
		client.SetStartupData([]common.PathInfo{}, map[string]string{}, "test")

		// Add request to the counter
		client.RequestCounter.AddRequest("GET", "/test", "test", 200, 123, 0, 0)

		// Log request
		timestamp := float64(time.Now().Unix())
		request := &common.Request{
			Timestamp: timestamp,
			Method:    "GET",
			Path:      "/test",
			URL:       "http://test/test",
			Headers:   [][2]string{},
			Body:      []byte{},
		}
		response := &common.Response{
			StatusCode:   200,
			ResponseTime: 0.123,
			Headers:      [][2]string{},
			Body:         []byte{},
		}
		client.RequestLogger.LogRequest(request, response, nil, "")

		// Wait for request logger maintenance to run
		time.Sleep(time.Millisecond * 1100)

		// Trigger final sync
		client.Shutdown()

		recordedURLs := mockTransport.GetRecordedURLs()
		assert.True(t, slices.ContainsFunc(recordedURLs, func(url string) bool {
			return strings.HasSuffix(url, "/test/startup")
		}))
		assert.True(t, slices.ContainsFunc(recordedURLs, func(url string) bool {
			return strings.HasSuffix(url, "/test/sync")
		}))
		assert.True(t, slices.ContainsFunc(recordedURLs, func(url string) bool {
			return strings.Contains(url, "/test/log?uuid=")
		}))
	})
}

func createMockHTTPClient() (*retryablehttp.Client, *mockTransport) {
	client := getHttpClient()
	mockTransport := &mockTransport{}
	client.HTTPClient = &http.Client{
		Transport: mockTransport,
	}
	return client, mockTransport
}

type mockTransport struct {
	recordedURLs []string
	mutex        sync.Mutex
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Record the request URL
	m.mutex.Lock()
	m.recordedURLs = append(m.recordedURLs, req.URL.String())
	m.mutex.Unlock()

	// Always return 202 Accepted
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}
	return resp, nil
}

func (m *mockTransport) GetRecordedURLs() []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.recordedURLs
}
