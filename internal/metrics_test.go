package internal

import (
	"context"
	"errors"
	"log/slog"
	"maps"
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

func TestExcludedAndSampledOutRequestsAreStillCounted(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.SampleRate = 0
	registerForTest(t, server, cfg)
	fail := func(w http.ResponseWriter, r *http.Request) {
		CaptureError(r.Context(), errors.New("failed"))
		w.WriteHeader(http.StatusInternalServerError)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/items", writeOK)
	mux.HandleFunc("GET /orders", fail)
	mux.HandleFunc("GET /healthz", fail)
	appURL := startTestApp(t, mux)

	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/items")
	testutils.Get(t, appURL+"/orders")
	testutils.Get(t, appURL+"/healthz")
	testutils.Get(t, appURL+"/missing")
	req, _ := http.NewRequest(http.MethodOptions, appURL+"/items", nil)
	testutils.Do(t, http.DefaultClient.Do, req)
	require.NoError(t, Shutdown(context.Background()))

	assert.Empty(t, server.Spans(t))
	metrics := server.Metrics(t)
	var points []any
	for _, point := range testutils.HistogramPoints(metrics, "http.server.request.duration") {
		points = append(points, []any{testutils.Attributes(point.Attributes), point.Count})
	}
	point := func(route string, count uint64, statusAttributes map[string]any) []any {
		attrs := map[string]any{"http.request.method": "GET", "http.route": route, "url.scheme": "http"}
		maps.Copy(attrs, statusAttributes)
		return []any{attrs, count}
	}
	succeeded := map[string]any{"http.response.status_code": int64(200)}
	failed := map[string]any{"http.response.status_code": int64(500), "error.type": "500"}
	assert.ElementsMatch(t, []any{point("/items", 2, succeeded), point("/orders", 1, failed), point("/healthz", 1, failed)}, points)
	assert.Len(t, testutils.HistogramPoints(metrics, "http.server.request.body.size"), 3)
	assert.Len(t, testutils.HistogramPoints(metrics, "http.server.response.body.size"), 3)
	var errorPaths []any
	for _, record := range server.Events(t, serverErrorEventName) {
		errorPaths = append(errorPaths, testutils.Value(record.Body).(map[string]any)["path"])
	}
	assert.ElementsMatch(t, []any{"/orders", "/healthz"}, errorPaths)
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

func TestMetricCombinationsAreCappedAndSplit(t *testing.T) {
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
