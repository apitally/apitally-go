package internal

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestRequestMetricsCountExcludedAndSampledOutButNotUnmatchedRequests(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleRate = 0
	registerForTest(t, server, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/items", writeOK)
	mux.HandleFunc("GET /healthz", writeOK)
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/healthz")
	testutils.Get(t, appURL+"/missing")
	req, _ := http.NewRequest(http.MethodOptions, appURL+"/items", nil)
	testutils.Do(t, http.DefaultClient.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	assert.Empty(t, server.Spans(t))
	metrics := server.Metrics(t)
	points := testutils.HistogramPoints(metrics, "http.server.request.duration")
	require.Len(t, points, 2)
	counts := map[string]uint64{}
	for _, point := range points {
		attrs := testutils.Attributes(point.Attributes)
		counts[attrs["http.route"].(string)] = point.Count
		assert.Equal(t, map[string]any{
			"http.request.method":       "GET",
			"http.route":                attrs["http.route"],
			"http.response.status_code": int64(200),
			"url.scheme":                "http",
		}, attrs)
	}
	assert.Equal(t, map[string]uint64{"/items": 2, "/healthz": 1}, counts)
	assert.Len(t, testutils.HistogramPoints(metrics, "http.server.request.body.size"), 2)
	assert.Len(t, testutils.HistogramPoints(metrics, "http.server.response.body.size"), 2)
}

func TestRequestMetricsAreDeltasBetweenCollections(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	synctest.Test(t, func(t *testing.T) {
		registerForTest(t, server, nil)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /items", writeOK)
		app := NetHTTPMiddleware(serveMuxRoute)(mux)
		get := func() { app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil)) }

		get()
		time.Sleep(initialExportDelay + time.Second)
		get()
		get()
		require.NoError(t, Shutdown(context.Background()))

		points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.duration")
		require.Len(t, points, 2)
		assert.Equal(t, uint64(1), points[0].Count)
		assert.Equal(t, uint64(2), points[1].Count)
		assert.Equal(t, points[0].TimeUnixNano, points[1].StartTimeUnixNano)
	})
}

func TestMetricCombinationsAreCappedPerIntervalAndSplitIntoRequests(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	startRuntimeForTest(t, server, nil)
	logs := testutils.RecordSlog(t)
	m := currentRuntime.Load().metrics

	for i := range maxMetricCombinations + 1 {
		m.recordRequest(requestMetricKey{method: "GET", route: "/items/" + strconv.Itoa(i), statusCode: 200, scheme: "http"}, time.Millisecond, 0, 2)
	}
	require.NoError(t, Shutdown(context.Background()))

	var durationMetrics, points int
	for _, metric := range server.Metrics(t) {
		if metric.Name == "http.server.request.duration" {
			durationMetrics++
			points += len(metric.GetExponentialHistogram().DataPoints)
		}
	}
	assert.Equal(t, maxMetricCombinations/metricCombinationsPerRequest, durationMetrics)
	assert.Equal(t, maxMetricCombinations, points)
	assert.Len(t, logs.Messages(slog.LevelWarn), 1)
}
