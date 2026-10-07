package internal

import (
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// metrics aggregates Apitally's metrics, which the export worker collects
// every cycle.
type metrics struct {
	spool         *spool
	resource      *resourcepb.Resource
	mu            sync.Mutex
	intervalStart time.Time

	// Process observation state, used only while collecting.
	process     *process.Process
	startTime   time.Time
	lastTime    time.Time
	lastCPUTime float64
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
	m := &metrics{spool: sp, resource: res, intervalStart: now, startTime: now, lastTime: now}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		m.process = p
		m.lastCPUTime = processCPUTime(p)
	}
	return m
}

// collect appends the process gauges, which keep every collection non-empty
// as the liveness signal.
func (m *metrics) collect() {
	m.mu.Lock()
	start, end := m.intervalStart, time.Now()
	m.intervalStart = end
	m.mu.Unlock()
	m.spool.appendMessage(signalMetrics, encodeProcessGauges(m.resource, start, end, m.observeProcess(end)))
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
