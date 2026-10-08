package testutils

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// OTLPServer is a stub OTLP/HTTP endpoint that records export requests.
type OTLPServer struct {
	URL string

	mu             sync.Mutex
	requests       []ExportRequest
	status         int
	exportInterval int
	hold           chan struct{}
}

// ExportRequest is a recorded export request with the time it was received
// and the status the stub responded with. Body holds the request body as
// sent, gzip-compressed.
type ExportRequest struct {
	Signal string
	Header http.Header
	Body   []byte
	Status int
	Time   time.Time
}

// NewOTLPServer starts a stub endpoint and points APITALLY_OTLP_ENDPOINT and
// APITALLY_WRITE_TOKEN at it for the duration of the test.
func NewOTLPServer(t testing.TB) *OTLPServer {
	s := &OTLPServer{status: http.StatusOK}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	s.URL = server.URL
	t.Setenv("APITALLY_OTLP_ENDPOINT", server.URL)
	t.Setenv("APITALLY_WRITE_TOKEN", WriteToken)
	return s
}

// WriteToken is a valid write token for tests.
const WriteToken = "apt_abcdefghijklmnopqrstuvwx"

// Transport returns an http.RoundTripper that calls the stub's handler
// directly, for tests in testing/synctest bubbles, where idle network
// connections would block fake time.
func (s *OTLPServer) Transport() http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		s.ServeHTTP(recorder, r)
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return recorder.Result(), nil
	})
}

// SetResponse sets the status code of later responses and the
// Apitally-Export-Interval header value in seconds, omitted when zero.
func (s *OTLPServer) SetResponse(status, exportInterval int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.exportInterval = status, exportInterval
}

// Hold makes later requests wait until release is called or their context
// ends. Held requests are not recorded.
func (s *OTLPServer) Hold() (release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hold := make(chan struct{})
	s.hold = hold
	return sync.OnceFunc(func() { close(hold) })
}

func (s *OTLPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	hold, status, interval := s.hold, s.status, s.exportInterval
	s.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	s.mu.Lock()
	s.requests = append(s.requests, ExportRequest{Signal: strings.TrimPrefix(r.URL.Path, "/v1/"), Header: r.Header.Clone(), Body: body, Status: status, Time: time.Now()})
	s.mu.Unlock()
	if interval != 0 {
		w.Header().Set("Apitally-Export-Interval", strconv.Itoa(interval))
	}
	w.WriteHeader(status)
}

// Requests returns the recorded requests.
func (s *OTLPServer) Requests() []ExportRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExportRequest(nil), s.requests...)
}

// Span is an exported span with its resource and instrumentation scope.
type Span struct {
	*tracepb.Span
	Resource *resourcepb.Resource
	Scope    string
}

// LogRecord is an exported log record with its resource and scope.
type LogRecord struct {
	*logspb.LogRecord
	Resource *resourcepb.Resource
	Scope    string
}

// Metric is an exported metric with its resource and scope.
type Metric struct {
	*metricspb.Metric
	Resource *resourcepb.Resource
	Scope    string
}

// Spans decodes all spans received in successful requests.
func (s *OTLPServer) Spans(t testing.TB) []Span {
	var spans []Span
	for _, data := range decodeRequests(t, s, "traces", &tracepb.TracesData{}) {
		for _, rs := range data.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, span := range ss.Spans {
					spans = append(spans, Span{Span: span, Resource: rs.Resource, Scope: ss.Scope.GetName()})
				}
			}
		}
	}
	return spans
}

// LogRecords decodes all log records received in successful requests.
func (s *OTLPServer) LogRecords(t testing.TB) []LogRecord {
	var records []LogRecord
	for _, data := range decodeRequests(t, s, "logs", &logspb.LogsData{}) {
		for _, rl := range data.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, record := range sl.LogRecords {
					records = append(records, LogRecord{LogRecord: record, Resource: rl.Resource, Scope: sl.Scope.GetName()})
				}
			}
		}
	}
	return records
}

// ApplicationLogRecords returns the log records captured from the
// application's slog records, which are exported with the scope "slog".
func (s *OTLPServer) ApplicationLogRecords(t testing.TB) []LogRecord {
	var records []LogRecord
	for _, record := range s.LogRecords(t) {
		if record.Scope == "slog" {
			records = append(records, record)
		}
	}
	return records
}

// DecodeStartupEvent requires exactly one exported startup event and decodes
// its JSON body into body.
func (s *OTLPServer) DecodeStartupEvent(t testing.TB, body any) {
	t.Helper()
	records := s.Events(t, "apitally.app.startup")
	require.Len(t, records, 1)
	require.NoError(t, json.Unmarshal([]byte(records[0].Body.GetStringValue()), body))
}

// Events returns the SDK event log records with the given event name.
func (s *OTLPServer) Events(t testing.TB, eventName string) []LogRecord {
	var events []LogRecord
	for _, record := range s.LogRecords(t) {
		if record.EventName == eventName {
			events = append(events, record)
		}
	}
	return events
}

// Metrics decodes all metrics received in successful requests.
func (s *OTLPServer) Metrics(t testing.TB) []Metric {
	var metrics []Metric
	for _, data := range decodeRequests(t, s, "metrics", &metricspb.MetricsData{}) {
		for _, rm := range data.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, metric := range sm.Metrics {
					metrics = append(metrics, Metric{Metric: metric, Resource: rm.Resource, Scope: sm.Scope.GetName()})
				}
			}
		}
	}
	return metrics
}

// Attributes converts OTLP attributes to a map of Go values: string, bool,
// int64, float64, []byte, []any, map[string]any or nil.
func Attributes(kvs []*commonpb.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[kv.Key] = Value(kv.Value)
	}
	return out
}

// Value converts an OTLP value to a Go value, as in Attributes.
func Value(v *commonpb.AnyValue) any {
	switch v := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		return v.BoolValue
	case *commonpb.AnyValue_IntValue:
		return v.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return v.DoubleValue
	case *commonpb.AnyValue_BytesValue:
		return v.BytesValue
	case *commonpb.AnyValue_ArrayValue:
		items := make([]any, len(v.ArrayValue.Values))
		for i, item := range v.ArrayValue.Values {
			items[i] = Value(item)
		}
		return items
	case *commonpb.AnyValue_KvlistValue:
		return Attributes(v.KvlistValue.Values)
	}
	return nil
}

// decodeRequests decodes the bodies of successful requests for a signal. Each
// body is a gzip stream of concatenated messages, which decode as one.
func decodeRequests[M proto.Message](t testing.TB, s *OTLPServer, signal string, template M) []M {
	t.Helper()
	var out []M
	for _, req := range s.Requests() {
		if req.Signal != signal || req.Status != http.StatusOK {
			continue
		}
		reader, err := gzip.NewReader(bytes.NewReader(req.Body))
		require.NoError(t, err)
		raw, err := io.ReadAll(reader)
		require.NoError(t, err)
		m := proto.Clone(template).(M)
		require.NoError(t, proto.Unmarshal(raw, m))
		out = append(out, m)
	}
	return out
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// HistogramPoints returns the data points of the exponential histograms
// with the given name.
func HistogramPoints(metrics []Metric, name string) []*metricspb.ExponentialHistogramDataPoint {
	var points []*metricspb.ExponentialHistogramDataPoint
	for _, metric := range metrics {
		if metric.Name == name {
			points = append(points, metric.GetExponentialHistogram().GetDataPoints()...)
		}
	}
	return points
}
