package internal

import (
	"os"

	"github.com/shirou/gopsutil/v4/process"
)

type ResourceUsage struct {
	CpuPercent float64 `json:"cpu_percent"`
	MemoryRss  int64   `json:"memory_rss"`
}

type ResourceMonitor struct {
	isFirstInterval bool
	process         *process.Process
}

func NewResourceMonitor() *ResourceMonitor {
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return nil
	}
	return &ResourceMonitor{
		isFirstInterval: true,
		process:         proc,
	}
}

func (r *ResourceMonitor) GetCpuMemoryUsage() *ResourceUsage {
	if r == nil || r.process == nil {
		return nil
	}

	cpuPercent, err := r.process.Percent(0)
	if err != nil {
		return nil
	}

	memInfo, err := r.process.MemoryInfo()
	if err != nil {
		return nil
	}

	if r.isFirstInterval {
		r.isFirstInterval = false
		return nil
	}

	return &ResourceUsage{
		CpuPercent: cpuPercent,
		MemoryRss:  int64(memInfo.RSS),
	}
}
