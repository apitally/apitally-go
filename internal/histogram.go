package internal

import (
	"math"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

const histogramScale = 3

var histogramScaleFactor = math.Ldexp(math.Log2E, histogramScale)

// exponentialHistogram is a base-2 exponential histogram of non-negative
// values at a fixed scale. Its buckets cover only the recorded index range.
type exponentialHistogram struct {
	count     uint64
	sum       float64
	min       float64
	max       float64
	zeroCount uint64
	offset    int32
	buckets   []uint64
}

func (h *exponentialHistogram) record(value float64) {
	if h.count == 0 {
		h.min, h.max = value, value
	}
	h.min, h.max = min(h.min, value), max(h.max, value)
	h.count++
	h.sum += value
	if value == 0 {
		h.zeroCount++
		return
	}
	index := histogramBucketIndex(value)
	switch {
	case h.buckets == nil:
		h.buckets, h.offset = []uint64{0}, index
	case index < h.offset:
		h.buckets = append(make([]uint64, h.offset-index), h.buckets...)
		h.offset = index
	case int(index-h.offset) >= len(h.buckets):
		h.buckets = append(h.buckets, make([]uint64, int(index-h.offset)-len(h.buckets)+1)...)
	}
	h.buckets[index-h.offset]++
}

// histogramBucketIndex matches OpenTelemetry's mapping, in which an exact
// power of two belongs to the bucket below its boundary.
func histogramBucketIndex(value float64) int32 {
	if frac, exp := math.Frexp(value); frac == 0.5 {
		return int32(exp-1)<<histogramScale - 1
	}
	return int32(math.Ceil(math.Log(value)*histogramScaleFactor)) - 1
}

func (h *exponentialHistogram) dataPoint() *metricspb.ExponentialHistogramDataPoint {
	return &metricspb.ExponentialHistogramDataPoint{
		Count:     h.count,
		Sum:       &h.sum,
		Min:       &h.min,
		Max:       &h.max,
		Scale:     histogramScale,
		ZeroCount: h.zeroCount,
		Positive:  &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: h.offset, BucketCounts: h.buckets},
	}
}
