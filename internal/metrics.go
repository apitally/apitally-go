package internal

import (
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

const (
	// Bounds memory when most requests form a new combination, for example
	// when a request ID is used as the consumer identifier.
	maxMetricCombinations = 50_000
	// Each combination's histograms stay in one request, because the server
	// joins them per request.
	metricCombinationsPerRequest = 1_000
)

// metrics aggregates Apitally's metrics, which the export worker collects
// every cycle.
type metrics struct {
	spool              *spool
	resource           *resourcepb.Resource
	mu                 sync.Mutex
	intervalStart      time.Time
	requests           map[requestMetricKey]*requestMetricValues
	isCapacityExceeded bool

	// Process observation state, used only while collecting.
	process     *process.Process
	startTime   time.Time
	lastTime    time.Time
	lastCPUTime float64
}

// requestMetricKey holds the data point attributes of the request histograms.
type requestMetricKey struct {
	method     string
	route      string
	statusCode int
	scheme     string
	consumer   string
}

type requestMetricValues struct {
	duration         exponentialHistogram
	requestBodySize  exponentialHistogram
	responseBodySize exponentialHistogram
}

type processMetricValues struct {
	cpuUtilization    float64
	hasCPUUtilization bool
	memoryUsage       int64
	hasMemoryUsage    bool
	uptime            float64
}

func newMetrics(sp *spool, res *resourcepb.Resource) *metrics {
	now := time.Now()
	m := &metrics{spool: sp, resource: res, intervalStart: now, requests: map[requestMetricKey]*requestMetricValues{}, startTime: now, lastTime: now}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		m.process = p
		m.lastCPUTime = processCPUTime(p)
	}
	return m
}

// recordRequest records a request's duration and body sizes in one lock
// acquisition, so they land in the same collection. Sizes are -1 when unknown.
func (m *metrics) recordRequest(key requestMetricKey, duration time.Duration, requestBodySize, responseBodySize int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	values := m.requests[key]
	if values == nil {
		if len(m.requests) >= maxMetricCombinations {
			m.isCapacityExceeded = true
			return
		}
		values = &requestMetricValues{}
		m.requests[key] = values
	}
	values.duration.record(duration.Seconds())
	if requestBodySize >= 0 {
		values.requestBodySize.record(float64(requestBodySize))
	}
	if responseBodySize >= 0 {
		values.responseBodySize.record(float64(responseBodySize))
	}
}

// collect appends the process gauges, which keep every collection non-empty
// as the liveness signal, followed by the request histograms recorded since
// the previous collection.
func (m *metrics) collect() {
	m.mu.Lock()
	start, end := m.intervalStart, time.Now()
	requests, isCapacityExceeded := m.requests, m.isCapacityExceeded
	m.intervalStart, m.requests, m.isCapacityExceeded = end, map[requestMetricKey]*requestMetricValues{}, false
	m.mu.Unlock()
	if isCapacityExceeded {
		warnOnce("metrics-capacity", "Apitally recorded more than 50,000 combinations of method, route, status code and consumer in one interval, some request metrics are missing")
	}
	m.spool.appendMessage(signalMetrics, encodeProcessGauges(m.resource, start, end, m.observeProcess(end)))
	keys := make([]requestMetricKey, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	for len(keys) > 0 {
		n := min(len(keys), metricCombinationsPerRequest)
		m.spool.appendMessage(signalMetrics, encodeRequestHistograms(m.resource, start, end, keys[:n], requests))
		keys = keys[n:]
	}
}

// observeProcess reports CPU utilization normalized across CPUs and the
// resident set size, when the platform provides them.
func (m *metrics) observeProcess(now time.Time) processMetricValues {
	values := processMetricValues{uptime: now.Sub(m.startTime).Seconds()}
	if m.process == nil {
		return values
	}
	if cpuTime := processCPUTime(m.process); cpuTime >= 0 && m.lastCPUTime >= 0 {
		if elapsed := now.Sub(m.lastTime).Seconds(); elapsed > 0 {
			values.cpuUtilization = min(max((cpuTime-m.lastCPUTime)/(elapsed*float64(runtime.NumCPU())), 0), 1)
			values.hasCPUUtilization = true
		}
		m.lastCPUTime, m.lastTime = cpuTime, now
	}
	if info, err := m.process.MemoryInfo(); err == nil {
		values.memoryUsage, values.hasMemoryUsage = int64(info.RSS), true
	}
	return values
}

func processCPUTime(p *process.Process) float64 {
	times, err := p.Times()
	if err != nil {
		return -1
	}
	return times.User + times.System
}
