package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistogramBucketIndexMatchesOpenTelemetryMapping(t *testing.T) {
	for value, index := range map[float64]int32{
		1:      -1,
		2:      7,
		0.5:    -9,
		1024:   79,
		1.5:    4,
		1.0001: 0,
		0.003:  -68,
	} {
		assert.Equal(t, index, histogramBucketIndex(value), "value %v", value)
	}
}

func TestHistogramGrowsBucketsInBothDirections(t *testing.T) {
	var h exponentialHistogram
	for _, value := range []float64{2, 0, 1.5, 4, 1.5} {
		h.record(value)
	}

	assert.Equal(t, uint64(5), h.count)
	assert.Equal(t, uint64(1), h.zeroCount)
	assert.Equal(t, 9.0, h.sum)
	assert.Equal(t, 0.0, h.min)
	assert.Equal(t, 4.0, h.max)
	assert.Equal(t, int32(4), h.offset)
	assert.Equal(t, []uint64{2, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, h.buckets)
}
