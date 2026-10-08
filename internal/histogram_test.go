package internal

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

func TestHistogramIsExportedWithExponentialBuckets(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", writeOK)
	appURL := startTestApp(t, mux)

	// At scale 3, a power of two 2^n falls into bucket 8n-1 and 3 into bucket 12.
	for _, size := range []int{2, 0, 1, 4, 3} {
		post(t, appURL+"/items", "text/plain", strings.Repeat("x", size))
	}
	require.NoError(t, Shutdown(context.Background()))

	points := testutils.HistogramPoints(server.Metrics(t), "http.server.request.body.size")
	require.Len(t, points, 1)
	point := points[0]
	assert.Equal(t, int32(3), point.Scale)
	assert.Equal(t, uint64(5), point.Count)
	assert.Equal(t, 10.0, point.GetSum())
	assert.Equal(t, 0.0, point.GetMin())
	assert.Equal(t, 4.0, point.GetMax())
	assert.Equal(t, uint64(1), point.ZeroCount)
	assert.Equal(t, int32(-1), point.Positive.Offset)
	assert.Equal(t, []uint64{1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1}, point.Positive.BucketCounts)
}
