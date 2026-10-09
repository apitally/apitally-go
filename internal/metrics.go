package internal

import (
	"maps"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
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
	m.spool.appendMessage(signalMetrics, encodeMetrics(m.resource, m.observeProcess(start, end)))
	for keys := range slices.Chunk(slices.Collect(maps.Keys(requests)), metricCombinationsPerRequest) {
		m.spool.appendMessage(signalMetrics, encodeRequestHistograms(m.resource, start, end, keys, requests))
	}
}

// observeProcess returns the process gauges: CPU utilization normalized
// across CPUs and the resident set size, when the platform provides them,
// and the uptime.
func (m *metrics) observeProcess(start, end time.Time) []*metricspb.Metric {
	var gauges []*metricspb.Metric
	gauge := func(name, unit string, point *metricspb.NumberDataPoint) {
		point.StartTimeUnixNano, point.TimeUnixNano = unixNano(start), unixNano(end)
		gauges = append(gauges, &metricspb.Metric{
			Name: name,
			Unit: unit,
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point}}},
		})
	}
	if m.process != nil {
		if cpuTime := processCPUTime(m.process); cpuTime >= 0 && m.lastCPUTime >= 0 {
			if elapsed := end.Sub(m.lastTime).Seconds(); elapsed > 0 {
				utilization := min(max((cpuTime-m.lastCPUTime)/(elapsed*float64(runtime.NumCPU())), 0), 1)
				gauge("process.cpu.utilization", "1", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: utilization}})
			}
			m.lastCPUTime, m.lastTime = cpuTime, end
		}
		if info, err := m.process.MemoryInfo(); err == nil {
			gauge("process.memory.usage", "By", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsInt{AsInt: int64(info.RSS)}})
		}
	}
	gauge("process.uptime", "s", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: end.Sub(m.startTime).Seconds()}})
	return gauges
}

func processCPUTime(p *process.Process) float64 {
	times, err := p.Times()
	if err != nil {
		return -1
	}
	return times.User + times.System
}
