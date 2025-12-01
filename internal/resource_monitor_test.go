package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResourceMonitor(t *testing.T) {
	t.Run("GetCpuMemoryUsage", func(t *testing.T) {
		monitor := NewResourceMonitor()
		assert.NotNil(t, monitor)

		// First call should return nil (establishing baseline)
		usage := monitor.GetCpuMemoryUsage()
		assert.Nil(t, usage)

		// Wait a bit for CPU usage to be measurable
		time.Sleep(100 * time.Millisecond)

		// Second call should return valid metrics
		usage = monitor.GetCpuMemoryUsage()
		assert.NotNil(t, usage)
		assert.GreaterOrEqual(t, usage.CpuPercent, 0.0)
		assert.Greater(t, usage.MemoryRss, int64(0))
	})
}
