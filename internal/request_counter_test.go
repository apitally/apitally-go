package internal

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestCounter(t *testing.T) {
	t.Run("Aggregation", func(t *testing.T) {
		rc := NewRequestCounter()

		// Add some requests
		for i := 0; i < 3; i++ {
			rc.AddRequest("consumer1", "GET", "/test", 200, 45.7, 0, 3789)
		}
		rc.AddRequest("consumer2", "POST", "/test", 201, 60.1, 2123, 0)

		// Get aggregated requests
		requests := rc.GetAndResetRequests()

		// Assert that we have the correct number of items
		assert.Len(t, requests, 2)

		// Create a map to make testing easier
		requestMap := make(map[string]RequestsItem)
		for _, item := range requests {
			key := item.Consumer + ":" + item.Method + ":" + item.Path + ":" + strconv.Itoa(item.StatusCode)
			requestMap[key] = item
		}

		// Assert metrics
		item1 := requestMap["consumer1:GET:/test:200"]
		assert.Equal(t, 3, item1.RequestCount)
		assert.Equal(t, int64(11367), item1.ResponseSizeSum)
		assert.Equal(t, 3, item1.ResponseSizes[3])
		assert.Equal(t, 3, item1.ResponseTimes[40])

		item2 := requestMap["consumer2:POST:/test:201"]
		assert.Equal(t, 1, item2.RequestCount)
		assert.Equal(t, int64(2123), item2.RequestSizeSum)
		assert.Equal(t, 1, item2.RequestSizes[2])
		assert.Equal(t, 1, item2.ResponseTimes[60])

		// Get and reset with no data
		requests2 := rc.GetAndResetRequests()
		assert.Len(t, requests2, 0)
	})
}
