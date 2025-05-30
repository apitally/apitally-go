package internal

import (
	"math"
	"sync"
)

type requestKey struct {
	Consumer   string
	Method     string
	Path       string
	StatusCode int
}

type RequestsItem struct {
	Consumer        string      `json:"consumer,omitempty"`
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	StatusCode      int         `json:"status_code"`
	RequestCount    int         `json:"request_count"`
	RequestSizeSum  int64       `json:"request_size_sum"`
	ResponseSizeSum int64       `json:"response_size_sum"`
	ResponseTimes   map[int]int `json:"response_times"`
	RequestSizes    map[int]int `json:"request_sizes"`
	ResponseSizes   map[int]int `json:"response_sizes"`
}

type RequestCounter struct {
	requestCounts    map[requestKey]int
	requestSizeSums  map[requestKey]int64
	responseSizeSums map[requestKey]int64
	responseTimes    map[requestKey]map[int]int
	requestSizes     map[requestKey]map[int]int
	responseSizes    map[requestKey]map[int]int
	mutex            sync.Mutex
}

func NewRequestCounter() *RequestCounter {
	return &RequestCounter{
		requestCounts:    make(map[requestKey]int),
		requestSizeSums:  make(map[requestKey]int64),
		responseSizeSums: make(map[requestKey]int64),
		responseTimes:    make(map[requestKey]map[int]int),
		requestSizes:     make(map[requestKey]map[int]int),
		responseSizes:    make(map[requestKey]map[int]int),
	}
}

func (rc *RequestCounter) AddRequest(consumer, method, path string, statusCode int, responseTime float64, requestSize, responseSize int64) {
	// Generate key
	key := requestKey{
		Consumer:   consumer,
		Method:     method,
		Path:       path,
		StatusCode: statusCode,
	}

	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	// Increment request count
	rc.requestCounts[key]++

	// Add response time
	if rc.responseTimes[key] == nil {
		rc.responseTimes[key] = make(map[int]int)
	}
	responseTimeMsBin := int(math.Floor(responseTime/10) * 10) // Rounded to nearest 10ms
	rc.responseTimes[key][responseTimeMsBin]++

	// Add request size
	if requestSize >= 0 {
		rc.requestSizeSums[key] += int64(requestSize)
		if rc.requestSizes[key] == nil {
			rc.requestSizes[key] = make(map[int]int)
		}
		requestSizeKbBin := int(math.Floor(float64(requestSize) / 1000)) // Rounded down to nearest KB
		rc.requestSizes[key][requestSizeKbBin]++
	}

	// Add response size
	if responseSize >= 0 {
		rc.responseSizeSums[key] += int64(responseSize)
		if rc.responseSizes[key] == nil {
			rc.responseSizes[key] = make(map[int]int)
		}
		responseSizeKbBin := int(math.Floor(float64(responseSize) / 1000)) // Rounded down to nearest KB
		rc.responseSizes[key][responseSizeKbBin]++
	}
}

func (rc *RequestCounter) GetAndResetRequests() []RequestsItem {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	data := make([]RequestsItem, 0, len(rc.requestCounts))

	for key, count := range rc.requestCounts {
		responseTimes := rc.responseTimes[key]
		if responseTimes == nil {
			responseTimes = make(map[int]int)
		}

		requestSizes := rc.requestSizes[key]
		if requestSizes == nil {
			requestSizes = make(map[int]int)
		}

		responseSizes := rc.responseSizes[key]
		if responseSizes == nil {
			responseSizes = make(map[int]int)
		}

		item := RequestsItem{
			Consumer:        key.Consumer,
			Method:          key.Method,
			Path:            key.Path,
			StatusCode:      key.StatusCode,
			RequestCount:    count,
			RequestSizeSum:  rc.requestSizeSums[key],
			ResponseSizeSum: rc.responseSizeSums[key],
			ResponseTimes:   responseTimes,
			RequestSizes:    requestSizes,
			ResponseSizes:   responseSizes,
		}
		data = append(data, item)
	}

	// Reset all maps
	rc.requestCounts = make(map[requestKey]int)
	rc.requestSizeSums = make(map[requestKey]int64)
	rc.responseSizeSums = make(map[requestKey]int64)
	rc.responseTimes = make(map[requestKey]map[int]int)
	rc.requestSizes = make(map[requestKey]map[int]int)
	rc.responseSizes = make(map[requestKey]map[int]int)

	return data
}
