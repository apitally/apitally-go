# v1 code review, round 3: simplification

Review of the `v1` branch at commit `a522bf1`. It covers simplification only: code that can be simpler, better organised or shorter, without changing the feature set. Five reviewers each covered one area:
- the request observation pipeline (P);
- the export pipeline and logs (E);
- configuration, lifecycle and public types (K);
- the framework integrations' production code (W);
- the test suites and test duplication across layers (X).

Every finding was prototyped as its own commit, and `GOTOOLCHAIN=go1.25.14 make check test` passes at the head of each branch. Each finding preserves user-observable behaviour: the same exported telemetry, public API, panic safety, concurrency guarantees and dependency floors. 23 findings were applied and 6 rejected. Together with the B1 fix, the applied changes remove 265 lines net.

Each finding has an ID, a status (`open`, `fixed`, `rejected` or `deferred`) and a class:
- **Class a:** a mechanical change. It is minimal, unambiguous and safe to apply without a decision.
- **Class b:** a trade-off that needs a decision.

Every finding shows the code before and after the change. The excerpts are taken from the prototype commits; `// ...` marks unchanged code that is left out.

Locations refer to commit `a522bf1`. The prototypes are on these branches, one commit per finding, with the finding ID as the first word of the commit message:
- `pi-agent-880c640f-a4ae-4ae`: P findings.
- `pi-agent-50ee6228-3f0e-40e`: E findings.
- `pi-agent-0ec0e0a4-e4b5-4bd`: K findings.
- `pi-agent-d40252a7-ac3e-4d2`: W findings.
- `pi-agent-b3aa4730-95a4-4d3`: X findings.

The branches overlap in places: `P3` duplicates W1 and W2, and `P6` duplicates W3. Apply the W commits and skip `P3` and `P6`. X1 touches 18 test files, so cherry-picking it after the other branches may conflict in `internal/*_test.go`.

Update the status as findings are addressed, and add a short note for anything rejected or deferred.

## Helpers added and removed

Most findings inline code or remove indirection. These findings add a named function, method or type:

| Finding | Adds | Replaces |
| --- | --- | --- |
| X1 | `OTLPServer.SingleSpan(t)` | `Spans` then `require.Len(..., 1)` then `spans[0]`, 86 times |
| X2 | `testutils.Send(t, method, url, body, headers...)` | hand-built `http.NewRequest` plus `Do`, 15 times, and the internal `post` helper |
| P1 | `bodyReader.sizeAndCapturedBody(declaredLength)` | the same rule written out in `nethttp.go` and `fasthttp.go` |
| P7 | `redaction.redactHeaderValues(name, values)` | `redactHeaderValue(name, value)` and the rule written out three times |
| W2 | the `fasthttpMessage` interface | two inline interface assertions on `any` |

These findings remove a helper or type: K1 (`callbacksSet`, `withoutCallbacks`), E1 (`exportOutcome`, `exportResponse`), E2 (`currentExportInterval`), E4 (`sleep`), E6 (`processMetricValues`, `encodeProcessGauges`), E7 (`logRecord.scopeName`), E9 (`ownedSlogAttrsAtDepth`), K5 (the local `path` type).

## Tests

### X1. Add `OTLPServer.SingleSpan` for tests that expect exactly one span

Status: fixed
Class: b (needs decision)
Note: Middle ground. Every call stands on its own line as `span := server.SingleSpan(t)` and is never inlined into an expression, so the exact-count precondition stays visible. Blank lines are kept. Applied to all 86 occurrences, -77 lines.

- **Where:** helper next to `Spans` in `internal/testutils/otlp_server.go:143`. The pattern appears 86 times, in all six framework test files, `chi-v5/sdk_test.go` and 11 `internal/*_test.go` files.
- **Now:** every test that expects one span repeats the same three steps: decode the spans, require exactly one, then index `spans[0]` on every later line.
- **Change:** a helper that requires exactly one span and returns it, following the existing `DecodeStartupEvent`, which already requires exactly one startup event.
- **Decision:** the exact-count check moves from the test body into the helper's name. The prototype also inlines the call where the span is used once. A middle ground keeps a `span := server.SingleSpan(t)` line in every test and never inlines it.
- **Note:** the prototype also deletes the blank line between `shutDown(t)` and the assertions in some tests. Keep those lines when applying the change.
- **Delta:** -155 (`f81a188`).

The helper:

```go
// SingleSpan requires exactly one span received in successful requests and
// returns it.
func (s *OTLPServer) SingleSpan(t testing.TB) Span {
	t.Helper()
	spans := s.Spans(t)
	require.Len(t, spans, 1)
	return spans[0]
}
```

Before (`chi-v5/middleware_test.go`):

```go
assert.Equal(t, "item 42", resp.Body)
spans := server.Spans(t)
require.Len(t, spans, 1)
assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, spans[0].Kind)
assert.Equal(t, "GET /items/{id}", spans[0].Name)
assert.Equal(t, "github.com/apitally/apitally-go/chi-v5", spans[0].Scope)
assert.Equal(t, map[string]any{
	// ...
}, testutils.Attributes(spans[0].Attributes))
```

After:

```go
assert.Equal(t, "item 42", resp.Body)
span := server.SingleSpan(t)
assert.Equal(t, tracepb.Span_SPAN_KIND_SERVER, span.Kind)
assert.Equal(t, "GET /items/{id}", span.Name)
assert.Equal(t, "github.com/apitally/apitally-go/chi-v5", span.Scope)
assert.Equal(t, map[string]any{
	// ...
}, testutils.Attributes(span.Attributes))
```

Where the span is used once, the prototype inlines the call:

```go
// Before
spans := server.Spans(t)
require.Len(t, spans, 1)
attrs := testutils.Attributes(spans[0].Attributes)

// After
attrs := testutils.Attributes(server.SingleSpan(t).Attributes)
```

### X2. Add `testutils.Send` for requests other than plain GETs

Status: fixed
Class: b (needs decision)
Note: Applied as prototyped: a string body and header pairs, matching `Get`. The internal `post` helper is removed.

- **Where:**
  - `internal/testutils/http.go:43` (`Get`).
  - Hand-built requests: `chi-v5/middleware_test.go:173,215,303`; `echo-v4/middleware_test.go:212,252,306`; `echo-v5/middleware_test.go:216,256,310`; `gin-v1/middleware_test.go:204,292`; `internal/body_processing_test.go:61`; `internal/metrics_test.go:42`; `internal/requests_test.go:167`; `internal/validation_errors_test.go:44`.
  - The `post` helper in `internal/body_capture_test.go:30`, which three other test files also use.
- **Now:** `Get` exists for GET requests. Every other method is built by hand in 2 to 5 lines, and the internal suite has its own `post` helper for POSTs with a content type.
- **Change:** `Send` takes the method and a string body, and `Get` calls it. Four framework test files drop their `strings` import. Fiber's tests already use a `send(t, app, method, url, body, headers...)` helper with the same shape.
- **Decision:** headers are passed as name and value pairs in a variadic `...string`, as `Get` already does. That is compact but untyped: an odd number of strings silently drops the last one.
- **Why it is safe:** with an empty string body, `http.NewRequest` sets `NoBody` and a `ContentLength` of 0, the same as a nil body.
- **Delta:** -25 (`2f16a1a`).

The helper, with `Get` delegating to it:

```go
// Get sends a GET request with http.DefaultClient and the request headers
// given as name and value pairs.
func Get(t testing.TB, url string, headers ...string) Response {
	t.Helper()
	return Send(t, http.MethodGet, url, "", headers...)
}

// Send sends a request with http.DefaultClient and the request headers given
// as name and value pairs.
func Send(t testing.TB, method, url, body string, headers ...string) Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return Do(t, http.DefaultClient.Do, req)
}
```

Before (two tests in `echo-v4/middleware_test.go`):

```go
// TestRequestAndResponseBodiesCapturedAndRedacted
req, _ := http.NewRequest(http.MethodPost, appURL+"/items", strings.NewReader(`{"name": "x", "password": "secret"}`))
req.Header.Set("Content-Type", "application/json")
resp := testutils.Do(t, http.DefaultClient.Do, req)

// TestUnmatchedRequestHasNoRouteAndNoHistogramPoint
req, _ := http.NewRequest(http.MethodDelete, appURL+"/items/1", nil)
notAllowed := testutils.Do(t, http.DefaultClient.Do, req)
```

After:

```go
// TestRequestAndResponseBodiesCapturedAndRedacted
resp := testutils.Send(t, http.MethodPost, appURL+"/items", `{"name": "x", "password": "secret"}`, "Content-Type", "application/json")

// TestUnmatchedRequestHasNoRouteAndNoHistogramPoint
notAllowed := testutils.Send(t, http.MethodDelete, appURL+"/items/1", "")
```

Before (`internal/body_capture_test.go`):

```go
func post(t *testing.T, url, contentType, body string) {
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	testutils.Do(t, http.DefaultClient.Do, req)
}

post(t, appURL+"/json", "Application/JSON", `{"name":"x"}`)
```

After:

```go
testutils.Send(t, http.MethodPost, appURL+"/json", `{"name":"x"}`, "Content-Type", "Application/JSON")
```

### X4. Cover Gin's `WriteString` in the streaming scenario and delete its separate test

Status: fixed
Class: a (mechanical)

- **Where:** `gin-v1/middleware_test.go:58-63` (the `/stream` handler) and `:385-406` (`TestWriteStringIsCountedAndCapturedOnce`).
- **Now:** `TestWriteStringIsCountedAndCapturedOnce` is a separate micro-test. The canonical streaming test already asserts the captured body, its size and the histogram sum, but its handler only calls `Write`.
- **Change:** the `/stream` handler writes through `WriteString`, and the micro-test is deleted. The bodies test still covers `Write` through `c.Data`.
- **Why it is safe:** if `WriteString` bypasses the observed writer, `TestStreamingResponseSizeAndBodyCaptured` fails with size 0. If it is counted twice, the test fails with size 48.
- **Delta:** -21 (`1977f86`).

Before:

```go
r.GET("/stream", func(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	for i := range 3 {
		_, _ = fmt.Fprintf(c.Writer, "chunk %d\n", i)
		c.Writer.Flush()
	}
})

// ...

func TestWriteStringIsCountedAndCapturedOnce(t *testing.T) {
	// 22 lines: a separate engine whose handler calls WriteString("hello ")
	// then Write("world"), and asserts the body and a size of 11.
}
```

After:

```go
r.GET("/stream", func(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	for i := range 3 {
		// gin.ResponseWriter writes through WriteString as well as Write.
		_, _ = c.Writer.WriteString(fmt.Sprintf("chunk %d\n", i))
		c.Writer.Flush()
	}
})
```

### X5. Filter exported log records with `slices.DeleteFunc`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/testutils/otlp_server.go:174` (`ApplicationLogRecords`) and `:194` (`Events`).
- **Change:** replace each hand-written filter loop with `slices.DeleteFunc`. `LogRecords` returns a fresh slice, so modifying it in place is safe. No caller distinguishes a nil result from an empty one.
- **Note:** `DeleteFunc` takes the condition for removal, so the comparison is inverted (`!=`). This is less direct to read than a loop that keeps matching records.
- **Delta:** -11 (`76ae8e8`).

Before:

```go
func (s *OTLPServer) Events(t testing.TB, eventName string) []LogRecord {
	var events []LogRecord
	for _, record := range s.LogRecords(t) {
		if record.EventName == eventName {
			events = append(events, record)
		}
	}
	return events
}
```

After:

```go
func (s *OTLPServer) Events(t testing.TB, eventName string) []LogRecord {
	return slices.DeleteFunc(s.LogRecords(t), func(record LogRecord) bool { return record.EventName != eventName })
}
```

### X3. Pass `t.Context()` to `Shutdown` in the framework tests

Status: fixed
Class: a (mechanical)

- **Where:** the `shutDown` helper in every framework `middleware_test.go`.
- **Change:** this removes the file's only use of the `context` import. `shutDown` always runs in the test body, before `t.Context()` is cancelled.
- **Delta:** -6 (`bb3e439`).

```go
// Before
func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(context.Background()))
}

// After
func shutDown(t *testing.T) {
	require.NoError(t, apitally.Shutdown(t.Context()))
}
```

## Export pipeline and logs

### E1. `exportClient.post` returns the status and interval instead of a classified outcome

Status: fixed
Class: a (mechanical)

- **Where:** `internal/export_client.go:21-27` (`exportOutcome`), `:42-46` (`exportResponse`), `:71-98` (`post`); `internal/export_worker.go:101-113`.
- **Now:** `post` maps each response to a three-value enum and returns it in a struct along with the status and interval. The worker switches on the enum and reads the status back to log the warning. The enum and struct have no other use.
- **Change:** `post` returns the status, with 0 when no response arrived, and the interval. The worker decides the outcome in one switch.
- **Why it is safe:** every status maps to the same outcome as before, including 1xx, which is still rejected.
- **Delta:** -22 (`cead83f`).

Before:

```go
type exportOutcome int

const (
	exportAccepted exportOutcome = iota
	exportRetryable
	exportRejected
)

type exportResponse struct {
	outcome  exportOutcome
	status   int
	interval time.Duration
}

func (c *exportClient) post(ctx context.Context, signal string, body []byte) exportResponse {
	// ...
	if err != nil {
		logDebug("Apitally could not send buffered "+signal+", will retry", "error", err)
		return exportResponse{outcome: exportRetryable}
	}
	// ...
	result := exportResponse{status: resp.StatusCode}
	if seconds, err := strconv.Atoi(resp.Header.Get(exportIntervalHeader)); err == nil {
		result.interval = min(max(time.Duration(seconds)*time.Second, minExportInterval), maxExportInterval)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.outcome = exportAccepted
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		result.outcome = exportRetryable
	default:
		result.outcome = exportRejected
	}
	return result
}

// In sendPendingFiles:
resp := r.client.post(ctx, f.signal, body)
if resp.interval > 0 {
	r.exportInterval = resp.interval
}
switch resp.outcome {
case exportAccepted:
	r.spool.delete(f)
case exportRejected:
	warnOnce("export-rejected-"+strconv.Itoa(resp.status), "Apitally rejected buffered "+f.signal+" with HTTP status "+strconv.Itoa(resp.status)+" and they were dropped")
	r.spool.delete(f)
default:
	return
}
```

After:

```go
// post returns the response status, or 0 when no response arrived, and the
// export interval the response requests, or 0.
func (c *exportClient) post(ctx context.Context, signal string, body []byte) (status int, interval time.Duration) {
	// ...
	if err != nil {
		logDebug("Apitally could not send buffered "+signal+", will retry", "error", err)
		return 0, 0
	}
	// ...
	if seconds, err := strconv.Atoi(resp.Header.Get(exportIntervalHeader)); err == nil {
		interval = min(max(time.Duration(seconds)*time.Second, minExportInterval), maxExportInterval)
	}
	return resp.StatusCode, interval
}

// In sendPendingFiles:
status, interval := r.client.post(ctx, f.signal, body)
if interval > 0 {
	r.exportInterval = interval
}
switch {
case status == 0 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500:
	return
case status < 200 || status >= 300:
	warnOnce("export-rejected-"+strconv.Itoa(status), "Apitally rejected buffered "+f.signal+" with HTTP status "+strconv.Itoa(status)+" and they were dropped")
}
r.spool.delete(f)
```

### E6. Build the process gauges in `observeProcess`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/metrics.go:54-60` (`processMetricValues`), `:107`, `:119-138` (`observeProcess`); `internal/otlp_encoding.go:71-89` (`encodeProcessGauges`).
- **Now:** `observeProcess` fills a struct of values with `has*` flags. `encodeProcessGauges` then checks the same flags again to decide which gauges to emit.
- **Change:** `observeProcess` builds the gauges directly, using the `gauge` closure from `encodeProcessGauges`. `histogram.go` already builds `metricspb` values, so `metrics.go` follows existing practice.
- **Why it is safe:** the same gauges are emitted in the same order with the same timestamps. `TestIdleShutdownDeliversProcessGauges` pins this.
- **Delta:** -18 (`94224dd`).

Before:

```go
type processMetricValues struct {
	cpuUtilization    float64
	hasCPUUtilization bool
	memoryUsage       int64
	hasMemoryUsage    bool
	uptime            float64
}

// In collect:
m.spool.appendMessage(signalMetrics, encodeProcessGauges(m.resource, start, end, m.observeProcess(end)))

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

// In otlp_encoding.go:
func encodeProcessGauges(res *resourcepb.Resource, start, end time.Time, values processMetricValues) *metricspb.MetricsData {
	var metrics []*metricspb.Metric
	gauge := func(name, unit string, point *metricspb.NumberDataPoint) { /* ... */ }
	if values.hasCPUUtilization {
		gauge("process.cpu.utilization", "1", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: values.cpuUtilization}})
	}
	if values.hasMemoryUsage {
		gauge("process.memory.usage", "By", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsInt{AsInt: values.memoryUsage}})
	}
	gauge("process.uptime", "s", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: values.uptime}})
	return encodeMetrics(res, metrics)
}
```

After:

```go
// In collect:
m.spool.appendMessage(signalMetrics, encodeMetrics(m.resource, m.observeProcess(start, end)))

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
```

### E7. `logRecord` stores the `trace.SpanContext` and `runtime.Frame` instead of copying their fields

Status: fixed
Class: a (mechanical)

- **Where:** `internal/log_batcher.go:27-41`; `internal/slog_handler.go:108-114`; `internal/otlp_encoding.go:31-37,51-57,65-67`.
- **Now:** `capture` copies three span context fields and three frame fields into `logRecord`, and `encodeLogRecord` reads them back. The single-use `logRecord.scopeName` method lives in `log_batcher.go` but only matters to encoding. `encodeLogs` finds the scope with a hand-written index loop.
- **Change:** `logRecord` keeps the two values whole. `encodeLogs` picks the scope name inline and finds the scope with `slices.IndexFunc`.
- **Why it is safe:** the encoded IDs, flags and code attributes are byte-identical. Code attributes are still emitted only when `Function` is set.
- **Delta:** -12 (`0731456`).

Before:

```go
type logRecord struct {
	record       root.LogRecord
	eventName    string
	eventBody    attribute.Value
	traceID      trace.TraceID
	spanID       trace.SpanID
	traceFlags   trace.TraceFlags
	serverSpanID trace.SpanID
	codeFunction string
	codeFile     string
	codeLine     int
}

func (r *logRecord) scopeName() string {
	if r.eventName != "" {
		return sdkScopeName
	}
	return "slog"
}

// In capture:
captured := &logRecord{
	record:       root.LogRecord{ /* ... */ },
	traceID:      spanCtx.TraceID(),
	spanID:       spanCtx.SpanID(),
	traceFlags:   spanCtx.TraceFlags(),
	serverSpanID: state.span.SpanContext().SpanID(),
	codeFunction: frame.Function,
	codeFile:     frame.File,
	codeLine:     frame.Line,
}

// In encodeLogs:
scopeName := r.scopeName()
i := 0
for i < len(scopes) && scopes[i].Scope.Name != scopeName {
	i++
}
if i == len(scopes) {
	scopes = append(scopes, &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: scopeName}})
}
```

After:

```go
type logRecord struct {
	record       root.LogRecord
	eventName    string
	eventBody    attribute.Value
	spanContext  trace.SpanContext
	serverSpanID trace.SpanID
	frame        runtime.Frame
}

// In capture:
captured := &logRecord{
	record:       root.LogRecord{ /* ... */ },
	spanContext:  spanCtx,
	serverSpanID: state.span.SpanContext().SpanID(),
	frame:        frame,
}

// In encodeLogs:
scopeName := "slog"
if r.eventName != "" {
	scopeName = sdkScopeName
}
i := slices.IndexFunc(scopes, func(s *logspb.ScopeLogs) bool { return s.Scope.Name == scopeName })
if i < 0 {
	i = len(scopes)
	scopes = append(scopes, &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: scopeName}})
}

// In encodeLogRecord, r.codeFunction becomes r.frame.Function, and so on, and:
traceID, spanID := r.spanContext.TraceID(), r.spanContext.SpanID()
// ...
	TraceId: traceID[:],
	SpanId:  spanID[:],
	Flags:   uint32(r.spanContext.TraceFlags()),
```

### E3. Chunk and filter export batches with `slices.Chunk`, `maps.Keys` and `slices.DeleteFunc`

Status: fixed
Class: a (mechanical)
Note: The histogram filter is written as separate statements: build the slice, call `DeleteFunc` on it, then pass it to `encodeMetrics`. It is not nested inside the `encodeMetrics` call.

- **Where:** `internal/span_exporter.go:117-121`, `internal/log_batcher.go:144-148`, `internal/metrics.go:108-116`, `internal/otlp_encoding.go:126-132`.
- **Now:** the span, log and metric paths each contain the same hand-written chunking loop. The metrics path also collects the map keys by hand, and the histogram encoder filters out empty metrics with an append loop.
- **Change:** standard library iterators and filters, all available since Go 1.23.
- **Delta:** -10 (`2b2eb9a`).

Before:

```go
// log_batcher.go; span_exporter.go has the same loop
for len(batch) > 0 {
	n := min(len(batch), recordsPerEncodedChunk)
	b.spool.appendMessage(signalLogs, encodeLogs(b.resource, batch[:n]))
	batch = batch[n:]
}

// metrics.go
keys := make([]requestMetricKey, 0, len(requests))
for key := range requests {
	keys = append(keys, key)
}
for len(keys) > 0 {
	n := min(len(keys), metricCombinationsPerRequest)
	m.spool.appendMessage(signalMetrics, encodeRequestHistograms(m.resource, start, end, keys[:n], requests))
	keys = keys[n:]
}

// otlp_encoding.go
var metrics []*metricspb.Metric
for _, metric := range []*metricspb.Metric{duration, requestBodySize, responseBodySize} {
	if len(metric.GetExponentialHistogram().DataPoints) > 0 {
		metrics = append(metrics, metric)
	}
}
return encodeMetrics(res, metrics)
```

After:

```go
// log_batcher.go; span_exporter.go has the same loop
for chunk := range slices.Chunk(batch, recordsPerEncodedChunk) {
	b.spool.appendMessage(signalLogs, encodeLogs(b.resource, chunk))
}

// metrics.go
for keys := range slices.Chunk(slices.Collect(maps.Keys(requests)), metricCombinationsPerRequest) {
	m.spool.appendMessage(signalMetrics, encodeRequestHistograms(m.resource, start, end, keys, requests))
}

// otlp_encoding.go
metrics := []*metricspb.Metric{duration, requestBodySize, responseBodySize}
metrics = slices.DeleteFunc(metrics, func(metric *metricspb.Metric) bool {
	return len(metric.GetExponentialHistogram().DataPoints) == 0
})
return encodeMetrics(res, metrics)
```

### E8. Drop the unreachable non-copy branch from `spanExporter.process`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/span_exporter.go:110-115,129-157`.
- **Now:**
  - `process` returns `(ReadOnlySpan, bool)` and handles spans that are not `*exportSpan`. That branch cannot run, because `release` is the only caller of `batchProcessor.OnEnd` and it passes only export copies.
  - `process` also has its own recover closure, which repeats `recoverAndLogPanic` and sets `ok = false`.
- **Change:** `process` returns only `ok`, asserts the type directly and uses the shared recover helper.
- **Why it is safe:**
  - The same log line is written on panic, and the named result `ok` stays false.
  - If a non-copy span ever arrived, the failed assertion would be recovered and the span dropped, the same result as today.
- **Note:** the prototype commit also removes `isKept = false` from the recover in `callMaskLogRecord`. Do not apply that part; see E10.
- **Delta:** about -7 (`df61d30`, which is -8 including the rejected part).

Before:

```go
for _, span := range spans {
	if c, ok := e.process(span); ok {
		processed = append(processed, c)
	}
}

// process drops a span whose redaction fails, so it never leaves the process
// unredacted.
func (e *spanExporter) process(span sdktrace.ReadOnlySpan) (_ sdktrace.ReadOnlySpan, ok bool) {
	defer func() {
		if p := recover(); p != nil {
			logPanic("span redaction", p)
			ok = false
		}
	}()
	c, isCopy := span.(*exportSpan)
	if !isCopy {
		return nil, false
	}
	c.attributes = e.redaction.redactSpanAttributes(c.attributes)
	// ...
	return c, true
}
```

After:

```go
for _, span := range spans {
	if e.process(span) {
		processed = append(processed, span)
	}
}

// process redacts an export copy. It returns false when redaction fails, so
// the span never leaves the process unredacted.
func (e *spanExporter) process(span sdktrace.ReadOnlySpan) (ok bool) {
	defer recoverAndLogPanic("span redaction")
	// Request release passes only export copies to the batch processor.
	c := span.(*exportSpan)
	c.attributes = e.redaction.redactSpanAttributes(c.attributes)
	// ...
	return true
}
```

### E4. Inline the pause between sends

Status: fixed
Class: a (mechanical)

- **Where:** `internal/export_worker.go:94-96,132-141`.
- **Change:** this replaces the single-use `sleep` helper. Since Go 1.23, timers that are no longer referenced are garbage collected, so `time.After` does not leak. The synctest budget test still passes.
- **Delta:** -7 (`d325eb4`).

Before:

```go
if budget >= 0 && sent > 0 && !sleep(ctx, time.Duration(100+rand.IntN(400))*time.Millisecond) {
	return
}

// ...

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
```

After:

```go
if budget >= 0 && sent > 0 {
	select {
	case <-time.After(time.Duration(100+rand.IntN(400)) * time.Millisecond):
	case <-ctx.Done():
		return
	}
}
```

### E9. Merge `ownedSlogAttrsAtDepth` into `ownedSlogAttrs`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/slog_handler.go:186-209`.
- **Change:** `ownedSlogAttrs` only calls `ownedSlogAttrsAtDepth` with depth 0. One function with a depth parameter replaces both, and its two callers pass 0. `maxSlogValueDepth` moves into the file's `const` block.
- **Delta:** -5 (`1c044bc`).

Before:

```go
func ownedSlogAttrs(attrs []slog.Attr) []slog.Attr {
	return ownedSlogAttrsAtDepth(attrs, 0)
}

// maxSlogValueDepth bounds the conversion of values that contain themselves.
const maxSlogValueDepth = 100

func ownedSlogAttrsAtDepth(attrs []slog.Attr, depth int) []slog.Attr {
	// ...
}

// Callers:
attrs = ownedSlogAttrs(attrs)
return slog.GroupValue(ownedSlogAttrsAtDepth(v.Group(), depth+1)...)
```

After:

```go
// ... strings. depth counts the enclosing values, so values that contain
// themselves stop at maxSlogValueDepth.
func ownedSlogAttrs(attrs []slog.Attr, depth int) []slog.Attr {
	// ...
}

// Callers:
attrs = ownedSlogAttrs(attrs, 0)
return slog.GroupValue(ownedSlogAttrs(v.Group(), depth+1)...)
```

### E2. Store the export interval in an `atomic.Int64`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/export_worker.go:61,104,125-129`; `internal/runtime.go:65,178`.
- **Now:** `currentExportInterval` takes `cycleLock` only to read one duration. As a side effect, the export loop cannot reschedule its timer until any `Flush` that is still delivering has finished.
- **Change:** an atomic replaces the lock-guarded field.
- **Why it is safe:** an interval requested by the server still takes effect from the next cycle, and the jitter is unchanged.
- **Delta:** -4 (`0e012bc`).

Before:

```go
// sdkRuntime
exportInterval time.Duration

// runExportLoop
timer.Reset(time.Duration(float64(r.currentExportInterval()) * (0.9 + 0.2*rand.Float64())))

// sendPendingFiles
r.exportInterval = interval

func (r *sdkRuntime) currentExportInterval() time.Duration {
	r.cycleLock <- struct{}{}
	defer func() { <-r.cycleLock }()
	return r.exportInterval
}
```

After:

```go
// sdkRuntime
// exportInterval holds a time.Duration.
exportInterval atomic.Int64

// runExportLoop
timer.Reset(time.Duration(float64(r.exportInterval.Load()) * (0.9 + 0.2*rand.Float64())))

// sendPendingFiles
r.exportInterval.Store(int64(interval))
```

### E5. Flatten `spool.append` into early returns

Status: fixed
Class: a (mechanical)

- **Where:** `internal/spool.go:88-118`.
- **Now:** creating and writing the file happen inside an immediately invoked closure. After it returns, an `err`/`f != nil` check works out which step failed, and an `else` branch resets the logging flag.
- **Change:** a deferred size check, then plain early returns.
- **Why it is safe:** every path does the same steps in the same order. The deferred size check runs before the deferred unlock.
- **Delta:** -3 (`d73be21`).

Before:

```go
func (s *spool) append(signal string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDeleted {
		return
	}
	f := s.current[signal]
	if f != nil && f.uncompressedSize+len(payload) > maxUncompressedSpoolFileSize {
		s.closeCurrentFileLocked(signal)
		f = nil
	}
	err := func() (err error) {
		if f == nil {
			if f, err = s.createFile(signal); err != nil {
				return err
			}
			s.current[signal] = f
		}
		return f.write(payload)
	}()
	if err != nil {
		s.logWriteError(signal, err)
		if f != nil {
			delete(s.current, signal)
			f.delete()
		}
	} else {
		s.isWriteErrorLogged = false
	}
	s.enforceSizeLimitLocked()
}
```

After:

```go
func (s *spool) append(signal string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDeleted {
		return
	}
	defer s.enforceSizeLimitLocked()
	f := s.current[signal]
	if f != nil && f.uncompressedSize+len(payload) > maxUncompressedSpoolFileSize {
		s.closeCurrentFileLocked(signal)
		f = nil
	}
	if f == nil {
		var err error
		if f, err = s.createFile(signal); err != nil {
			s.logWriteError(signal, err)
			return
		}
		s.current[signal] = f
	}
	if err := f.write(payload); err != nil {
		s.logWriteError(signal, err)
		delete(s.current, signal)
		f.delete()
		return
	}
	s.isWriteErrorLogged = false
}
```

## Request observation pipeline

### P2. Merge the object and array branches of JSON body redaction

Status: rejected
Class: b (needs decision)
Note: The two branches follow JSON's grammar and read straight through. The duplication is in structure, not in a redaction rule. The merged version reuses one variable for the key and the value, and relies on decoder behaviour for a type assertion.

- **Where:** `internal/body_processing.go:114-164` (`writeRedactedJSON`).
- **Now:** the `{` and `[` branches repeat the same steps: write the opening delimiter, loop with commas, read the next token, recurse, then write and consume the closing delimiter. Only the object branch adds key handling and field redaction.
- **Change:** one loop for both containers, with the key step guarded by `opening == '{'`.
- **Decision:** the merged version is 12 lines shorter, but one variable, `value`, now holds the key token and then the value token, and the closing delimiter comes from a type assertion. The two separate branches are longer but each reads straight through.
- **Why it is safe:** tokens are read in the same order. A temporary comparison of old and new output on 22 inputs, including truncated and malformed JSON, a byte order mark and HTML characters, gave identical results. After `decoder.More()` returns false, `Token` returns either a closing delimiter or an error, so the assertion cannot fail.
- **Delta:** -12 (`1b46944`).

Before:

```go
func (red *redaction) writeRedactedJSON(decoder *json.Decoder, encoder *json.Encoder, out *bytes.Buffer, token json.Token) error {
	switch token {
	case json.Delim('{'):
		out.WriteByte('{')
		for i := 0; decoder.More(); i++ {
			if i > 0 {
				out.WriteByte(',')
			}
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, _ := keyToken.(string)
			if err := writeJSONScalar(encoder, out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if _, isString := valueToken.(string); isString && red.isBodyFieldRedacted(key) {
				valueToken = redactedValue
			}
			if err := red.writeRedactedJSON(decoder, encoder, out, valueToken); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case json.Delim('['):
		out.WriteByte('[')
		for i := 0; decoder.More(); i++ {
			if i > 0 {
				out.WriteByte(',')
			}
			itemToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := red.writeRedactedJSON(decoder, encoder, out, itemToken); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		return writeJSONScalar(encoder, out, token)
	}
	_, err := decoder.Token()
	return err
}
```

After:

```go
func (red *redaction) writeRedactedJSON(decoder *json.Decoder, encoder *json.Encoder, out *bytes.Buffer, token json.Token) error {
	opening, isDelim := token.(json.Delim)
	if !isDelim {
		return writeJSONScalar(encoder, out, token)
	}
	out.WriteByte(byte(opening))
	for i := 0; decoder.More(); i++ {
		if i > 0 {
			out.WriteByte(',')
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if opening == '{' {
			key, _ := value.(string)
			if err := writeJSONScalar(encoder, out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if value, err = decoder.Token(); err != nil {
				return err
			}
			if _, isString := value.(string); isString && red.isBodyFieldRedacted(key) {
				value = redactedValue
			}
		}
		if err := red.writeRedactedJSON(decoder, encoder, out, value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	out.WriteByte(byte(closing.(json.Delim)))
	return nil
}
```

### P7. Share one header redaction rule between captured headers and header span attributes

Status: fixed
Class: b (needs decision)
Note: Applied as prototyped. Having one definition of a privacy-relevant rule matters more than the `[]string{value})[0]` call site. P5 was applied with it, because P7 builds on it.

- **Where:** `internal/redaction.go:95-125` (`headerAttributes`, `redactHeaderValue`) and `:144-182` (`redactSpanAttribute`).
- **Now:** the rule "a redacted header becomes one `[REDACTED]` value, otherwise redact the query of `Location` and `Content-Location`" is written three times:
  - in `headerAttributes`;
  - in the STRING branch of `redactSpanAttribute`;
  - in the STRINGSLICE branch, which copies the values by hand and tracks `isChanged`.
- **Change:** `redactHeaderValues(name, values)` holds the whole rule and replaces `redactHeaderValue`.
- **Decision:** the rule then lives in one place. The STRING branch, however, has to wrap one value in a slice and take element 0 back out, which is less direct than the current per-value call.
- **Why it is safe:** every case produces the same values. `attribute.StringSlice` copies its input, and `AsStringSlice` returns a copy, so no shared slice is modified. `TestCapturedHeadersAreRedacted` covers all three paths.
- **Delta:** -10 (`07a75ed`, on top of P5).

Before:

```go
func (red *redaction) headerAttributes(prefix string, header http.Header) []attribute.KeyValue {
	// ... collect and sort names (see P5)
	for _, name := range names {
		values := make([]string, len(header[name]))
		for i, value := range header[name] {
			values[i] = red.redactHeaderValue(name, value)
		}
		if red.isHeaderRedacted(name) {
			values = []string{redactedValue}
		}
		attrs = append(attrs, attribute.StringSlice(prefix+strings.ToLower(name), values))
	}
	return attrs
}

// redactHeaderValue redacts the query of Location and Content-Location
// values, which are URLs.
func (red *redaction) redactHeaderValue(name, value string) string {
	switch strings.ToLower(strings.ReplaceAll(name, "_", "-")) {
	case "location", "content-location":
		return red.redactURLQuery(value)
	}
	return value
}

// In redactSpanAttribute:
case attribute.STRING:
	// ...
	switch {
	case isHeader && red.isHeaderRedacted(header):
		value = redactedValue
	case isHeader:
		value = red.redactHeaderValue(header, value)
	// ... url.query and url.full cases
	}
	return attribute.StringValue(value), value != original
case attribute.STRINGSLICE:
	original := kv.Value.AsStringSlice()
	if isHeader && red.isHeaderRedacted(header) {
		return attribute.StringSliceValue([]string{redactedValue}), true
	}
	values := make([]string, len(original))
	isChanged := false
	for i, item := range original {
		values[i] = item
		if isHeader {
			values[i] = red.redactHeaderValue(header, item)
		}
		isChanged = isChanged || values[i] != item
	}
	return attribute.StringSliceValue(values), isChanged
```

After:

```go
func (red *redaction) headerAttributes(prefix string, header http.Header) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(header))
	for _, name := range slices.Sorted(maps.Keys(header)) {
		attrs = append(attrs, attribute.StringSlice(prefix+strings.ToLower(name), red.redactHeaderValues(name, header[name])))
	}
	return attrs
}

// redactHeaderValues returns one value for a redacted header, and redacts
// the query of Location and Content-Location values, which are URLs.
func (red *redaction) redactHeaderValues(name string, values []string) []string {
	if red.isHeaderRedacted(name) {
		return []string{redactedValue}
	}
	switch strings.ToLower(strings.ReplaceAll(name, "_", "-")) {
	case "location", "content-location":
		redacted := make([]string, len(values))
		for i, value := range values {
			redacted[i] = red.redactURLQuery(value)
		}
		return redacted
	}
	return values
}

// In redactSpanAttribute:
case attribute.STRING:
	// ...
	switch {
	case isHeader:
		value = red.redactHeaderValues(header, []string{value})[0]
	// ... url.query and url.full cases
	}
	return attribute.StringValue(value), value != original
case attribute.STRINGSLICE:
	if !isHeader {
		return kv.Value, false
	}
	original := kv.Value.AsStringSlice()
	values := red.redactHeaderValues(header, original)
	return attribute.StringSliceValue(values), !slices.Equal(values, original)
```

### P5. Sort header names with `slices.Sorted(maps.Keys(...))`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/redaction.go:95-102`.
- **Delta:** -5 (`7227df8`).

```go
// Before
names := make([]string, 0, len(header))
for name := range header {
	names = append(names, name)
}
sort.Strings(names)
attrs := make([]attribute.KeyValue, 0, len(names))
for _, name := range names {

// After
attrs := make([]attribute.KeyValue, 0, len(header))
for _, name := range slices.Sorted(maps.Keys(header)) {
```

### P4. Embed `capturedError` in `serverErrorKey`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/server_errors.go:170-176`, `internal/requests.go:246`.
- **Change:** this matches how `validationErrorKey` already embeds `validationDetail`. The key compares the same fields, and `serverErrorEventBody` reads the promoted fields unchanged.
- **Delta:** -2 (`840fcb1`).

Before:

```go
type serverErrorKey struct {
	method     string
	path       string
	typeName   string
	message    string
	stacktrace string
}

r.serverErrors.add(serverErrorKey{method: method, path: path, typeName: captured.typeName, message: captured.message, stacktrace: captured.stacktrace}, consumerIdentifier)
```

After:

```go
type serverErrorKey struct {
	method string
	path   string
	capturedError
}

r.serverErrors.add(serverErrorKey{method: method, path: path, capturedError: *captured}, consumerIdentifier)
```

### P1. Move the body size and completeness rule into `bodyReader`

Status: rejected
Class: b (needs decision)
Note: The Fiber stream case is a simpler special case without a declared length, not a duplicate. Passing `-1` to mean "no declared length" hides that at the call site, and the change adds lines.

- **Where:** `internal/nethttp.go:82-88` (`Finish`), `internal/fasthttp.go:133-138` (`finish`), `internal/body_capture.go:66-90` (`bodyReader`).
- **Now:** both callers reach into `bodyReader`'s fields (`mu`, `isEOF`, `size`, `capture`) and each implements the same rule:
  - the size is the declared length, else the byte count at EOF, else -1;
  - the body is complete at EOF or once the declared length has been read.

  The net/http copy takes the mutex; the fasthttp copy does not.
- **Change:** a new method on `bodyReader` holds the rule and the locking. net/http passes `o.Request.ContentLength`. fasthttp passes `-1`, because it only wraps streams of unknown length.
- **Decision:** this puts the rule and its locking in the type that owns the mutex, but adds 4 lines and a method whose `-1` argument at the fasthttp call site needs the reader to know the rule.
- **Delta:** +4 (`c5d2f0e`).

The new method:

```go
// sizeAndCapturedBody returns the declared length, else the byte count at EOF,
// else -1, and the captured body once EOF or the declared length was read.
func (b *bodyReader) sizeAndCapturedBody(declaredLength int64) (int64, []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	size := declaredLength
	if size < 0 && b.isEOF {
		size = b.size
	}
	return size, b.capture.body(b.isEOF || b.size == declaredLength)
}
```

Before:

```go
// nethttp.go, Finish
if b := o.body; b != nil {
	b.mu.Lock()
	if result.RequestBodySize < 0 && b.isEOF {
		result.RequestBodySize = b.size
	}
	result.RequestBody = b.capture.body(b.isEOF || b.size == o.Request.ContentLength)
	b.mu.Unlock()
}

// fasthttp.go, finish
if s := o.stream; s != nil {
	if s.isEOF {
		result.ResponseBodySize = s.size
	}
	result.ResponseBody = s.capture.body(s.isEOF)
}
```

After:

```go
// nethttp.go, Finish
if o.body != nil {
	result.RequestBodySize, result.RequestBody = o.body.sizeAndCapturedBody(o.Request.ContentLength)
}

// fasthttp.go, finish
if o.stream != nil {
	result.ResponseBodySize, result.ResponseBody = o.stream.sizeAndCapturedBody(-1)
}
```

## Configuration, lifecycle and public types

### K1. Compare configurations field by field

Status: rejected
Class: b (needs decision)
Note: The explicit helpers state which fields are callbacks and how they compare. A forgotten new callback field only causes a spurious "different configuration" warning, which does not justify generic reflection to save 7 lines.

- **Where:** `internal/config.go:66-72` (`isEquivalent`), `:91-106` (`callbacksSet`, `withoutCallbacks`).
- **Now:** two single-use helpers each list the five callback fields by hand. One builds a `[5]bool` of which callbacks are set; the other copies the `Config` with the callbacks cleared. A new callback field has to be added to both, or configurations are compared wrongly.
- **Change:** one reflection loop over the fields of `Config`. A func field is equal when both values are nil or both are set; any other field is compared with `reflect.DeepEqual`. `Config` is flat and all its fields are exported, so `Interface()` cannot panic.
- **Decision:** generic reflection rather than an explicit list. It is shorter, and a new callback field needs no extra code. The explicit list says exactly which fields are callbacks, so it is easier to read at a glance.
- **Delta:** -7 (`7157411`).

Before:

```go
// isEquivalent compares resolved configurations. Callbacks are equal when both
// are set or both are unset, because funcs are not comparable and application
// factories create them inline.
func (s *settings) isEquivalent(other *settings) bool {
	a, b := s.config, other.config
	return callbacksSet(a) == callbacksSet(b) && reflect.DeepEqual(withoutCallbacks(a), withoutCallbacks(b))
}

func callbacksSet(c root.Config) [5]bool {
	return [5]bool{
		c.SampleOnRequest != nil,
		c.SampleOnResponse != nil,
		c.MaskRequestBody != nil,
		c.MaskResponseBody != nil,
		c.MaskLogRecord != nil,
	}
}

func withoutCallbacks(c root.Config) root.Config {
	c.SampleOnRequest, c.SampleOnResponse = nil, nil
	c.MaskRequestBody, c.MaskResponseBody = nil, nil
	c.MaskLogRecord = nil
	return c
}
```

After:

```go
// isEquivalent compares resolved configurations field by field. Callbacks are
// equal when both are set or both are unset, because funcs are not comparable
// and application factories create them inline.
func (s *settings) isEquivalent(other *settings) bool {
	a, b := reflect.ValueOf(s.config), reflect.ValueOf(other.config)
	for i := range a.NumField() {
		x, y := a.Field(i), b.Field(i)
		if x.Kind() == reflect.Func {
			if x.IsNil() != y.IsNil() {
				return false
			}
		} else if !reflect.DeepEqual(x.Interface(), y.Interface()) {
			return false
		}
	}
	return true
}
```

### K2. Keep the activation-only encoded resource and redaction as local variables

Status: fixed
Class: a (mechanical)

- **Where:** `internal/runtime.go:41,47` (fields), `:156-166` (`activate`), `:13` (`resourcepb` import).
- **Change:** `sdkRuntime.encodedResource` and `sdkRuntime.redaction` are read only inside `activate`, so they become local variables there. Both fields and the import are removed.
- **Delta:** -4 (`a0732d3`).

Before:

```go
// sdkRuntime fields
encodedResource   *resourcepb.Resource
redaction         *redaction

// activate
r.encodedResource = encodeResource(r.resource)
// ...
r.logs = newLogBatcher(r.spool, r.encodedResource)
r.metrics = newMetrics(r.spool, r.encodedResource)
r.redaction = newRedaction(r.settings)
// ...
r.batchProcessor = sdktrace.NewBatchSpanProcessor(newSpanExporter(r.redaction, r.settings, r.spool),
```

After:

```go
// activate
encodedResource := encodeResource(r.resource)
// ...
r.logs = newLogBatcher(r.spool, encodedResource)
r.metrics = newMetrics(r.spool, encodedResource)
// ...
r.batchProcessor = sdktrace.NewBatchSpanProcessor(newSpanExporter(newRedaction(r.settings), r.settings, r.spool),
```

### K4. Look up module versions in one loop

Status: rejected
Class: a (mechanical)
Note: The current code states \"main module first, then dependencies\" directly. The `append([]*debug.Module{&info.Main}, info.Deps...)` trick saves 5 lines but takes a moment to decode.

- **Where:** `internal/resource.go:43-57` (`moduleVersion`).
- **Change:** the main module is still checked first, followed by the dependencies.
- **Delta:** -5 (`2e7c04e`).

Before:

```go
func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Path == path {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			return strings.TrimPrefix(dep.Version, "v")
		}
	}
	return "unknown"
}
```

After:

```go
func moduleVersion(path string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, module := range append([]*debug.Module{&info.Main}, info.Deps...) {
			if module.Path == path {
				return strings.TrimPrefix(module.Version, "v")
			}
		}
	}
	return "unknown"
}
```

### K5. Encode the startup event's paths straight from `Route`

Status: fixed
Class: a (mechanical)

- **Where:** `internal/startup.go:26-29` (`Route`), `:77-87` (`startupEventBody`).
- **Change:** JSON tags on `Route` replace a local type that is identical apart from its tags. The JSON output is byte-identical.
- **Delta:** -4 (`fcb6ae4`).

Before:

```go
type Route struct {
	Method string
	Path   string
}

// In startupEventBody:
type path struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
paths := []path{}
// ...
		paths = append(paths, path(route))
```

After:

```go
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// In startupEventBody:
paths := []Route{}
// ...
		paths = append(paths, route)
```

### K3. Guard the route listers with the registration mutex

Status: fixed
Class: a (mechanical)

- **Where:** `internal/runtime.go:21,34-35,91-93,208-209`.
- **Now:** `Register` already holds `registrationMu` when it takes `routeListersMu` to append to `routeListers`. `listRoutes` takes `routeListersMu` again. Only registration writes the slice.
- **Change:** one mutex guards both.
- **Why it is safe:** `listRoutes` already blocked concurrent registrations through the shared `routeListersMu`, so blocking is unchanged. `Register` never calls `listRoutes` or `activate`, so there is no deadlock.
- **Delta:** -3 (`8a761ee`).

Before:

```go
var (
	registrationMu sync.Mutex
	// ...
)

type sdkRuntime struct {
	// ...
	routeListersMu sync.Mutex
	routeListers   []func() []Route
	// ...
}

// Register, while holding registrationMu:
r.routeListersMu.Lock()
r.routeListers = append(r.routeListers, listRoutes)
r.routeListersMu.Unlock()

func (r *sdkRuntime) listRoutes() []Route {
	r.routeListersMu.Lock()
	defer r.routeListersMu.Unlock()
	// ...
}
```

After:

```go
var (
	// registrationMu guards the creation of the runtime and its route listers.
	registrationMu sync.Mutex
	// ...
)

type sdkRuntime struct {
	// ...
	routeListers []func() []Route
	// ...
}

// Register, while holding registrationMu:
r.routeListers = append(r.routeListers, listRoutes)

func (r *sdkRuntime) listRoutes() []Route {
	registrationMu.Lock()
	defer registrationMu.Unlock()
	// ...
}
```

## Framework integrations

### W1. `BeginFasthttp` stores the request state itself and takes a typed `RequestCtx`

Status: fixed
Class: a (mechanical)

- **Where:** `fiber-v2/middleware.go:72-76`, `fiber-v3/middleware.go:72-75`, `internal/fasthttp.go:17-41,98`.
- **Now:**
  - Both Fiber middlewares call `c.Locals(internal.RequestStateKey, o.State)`, each behind its own nil guard and comment. `Locals` calls `SetUserValue` on the same `*fasthttp.RequestCtx` that `BeginFasthttp` already receives.
  - `BeginFasthttp` takes `requestCtx any` and asserts its type, recording the result in `isCloseRegistered`. Both callers always pass a `*fasthttp.RequestCtx`, so the flag is always true and the fallback it guards never runs.
- **Change:** a typed parameter, both user values set in `BeginFasthttp`, and the flag removed.
- **Why it is safe:** the same `SetUserValue` calls are made on the same `RequestCtx`, before `c.Next()`, only while Apitally is active. Checked on Fiber v2.51.0, v3.0.0 and v3.5.0. The compiler now checks the method set that used to be asserted at runtime.
- **Note:** P3 found the same change, without the `Locals` part.
- **Delta:** -8 (`14390e4`).

Before:

```go
// fiber-v2/middleware.go (fiber-v3 is the same, with its own comment)
o, ctx := internal.BeginFasthttp(c.UserContext(), c.Context(), requestInfo(c))
c.SetUserContext(ctx)
if o.State != nil {
	// Context.Value of fasthttp.RequestCtx resolves string keys to user values,
	// which include Locals.
	c.Locals(internal.RequestStateKey, o.State)
}

// internal/fasthttp.go
func BeginFasthttp(ctx context.Context, requestCtx any, info RequestInfo) (*FasthttpObservation, context.Context) {
	Activate()
	state, ctx := beginRequest(ctx, info)
	o := &FasthttpObservation{State: state}
	if userValues, ok := requestCtx.(interface{ SetUserValue(key, value any) }); ok && state != nil {
		userValues.SetUserValue(fasthttpObservationKey{}, o)
		o.isCloseRegistered = true
	}
	return o, ctx
}

// In FinishHandler:
isComplete := o.stream == nil || o.isClosed || !o.isCloseRegistered
```

After:

```go
// fiber-v2/middleware.go
o, ctx := internal.BeginFasthttp(c.UserContext(), c.Context(), requestInfo(c))
c.SetUserContext(ctx)

// internal/fasthttp.go
// BeginFasthttp activates Apitally and begins the request. requestCtx is the
// *fasthttp.RequestCtx, which closes the observation as a user value. The
// request state is also a user value, which Locals and the Value methods of
// the RequestCtx and fiber.Ctx resolve.
func BeginFasthttp(ctx context.Context, requestCtx interface{ SetUserValue(key, value any) }, info RequestInfo) (*FasthttpObservation, context.Context) {
	Activate()
	state, ctx := beginRequest(ctx, info)
	o := &FasthttpObservation{State: state}
	if state != nil {
		requestCtx.SetUserValue(RequestStateKey, state)
		requestCtx.SetUserValue(fasthttpObservationKey{}, o)
	}
	return o, ctx
}

// In FinishHandler:
isComplete := o.stream == nil || o.isClosed
```

### W2. Type `FinishHandler`'s request and response by method set

Status: fixed
Class: a (mechanical)

- **Where:** `internal/fasthttp.go:43-92`.
- **Now:** `request, response any` are checked against two inline interface assertions. A failed check silently skips size and body capture, and the response branch is nested one level deeper because of it. Fiber always passes `*fasthttp.Request` and `*fasthttp.Response`.
- **Change:** one interface types both parameters, which removes both assertions and the extra nesting. Both types have these methods at fasthttp v1.50.0 and v1.75.0.
- **Note:** P3 found the same change with two interfaces. W2's single interface is shorter.
- **Delta:** -1 (`59b8593`). The value is compile-time checking and less nesting.

Before:

```go
func (o *FasthttpObservation) FinishHandler(result TransportResult, request, response any, recovered any) {
	// ...
	if req, ok := request.(interface {
		IsBodyStream() bool
		Body() []byte
	}); ok && !req.IsBodyStream() {
		body := req.Body()
		// ...
	}
	// ...
	if resp, ok := response.(interface {
		IsBodyStream() bool
		BodyStream() io.Reader
		Body() []byte
	}); ok {
		if !resp.IsBodyStream() {
			// ...
		} else if size := streamSize(resp.BodyStream(), result.ResponseHeader); size >= 0 {
			// ...
		} else {
			// ...
		}
	}
	// ...
}
```

After:

```go
// fasthttpMessage is the body methods of *fasthttp.Request and
// *fasthttp.Response.
type fasthttpMessage interface {
	IsBodyStream() bool
	BodyStream() io.Reader
	Body() []byte
}

func (o *FasthttpObservation) FinishHandler(result TransportResult, request, response fasthttpMessage, recovered any) {
	// ...
	if !request.IsBodyStream() {
		body := request.Body()
		// ...
	}
	// ...
	if !response.IsBodyStream() {
		// ...
	} else if size := streamSize(response.BodyStream(), result.ResponseHeader); size >= 0 {
		// ...
	} else {
		// ...
	}
	// ...
}
```

### W3. `validationDetails` checks the error itself before unwrapping

Status: fixed
Class: a (mechanical)

- **Where:** `internal/validation_errors.go:53-74`.
- **Now:** a type switch with a `nil` case, which calls `fieldErrorDetails(err)` in the `Unwrap() error` case and the default case, but not in the `Unwrap() []error` case.
- **Change:** check the error itself once at the top, then unwrap. This is the order `errors.As` uses, which the doc comment already describes. `fieldErrorDetails(nil)` returns nil, so the `nil` case goes away.
- **Why it is safe:** results are the same for every existing error type. They would differ only for a slice of field errors that also implements `Unwrap() []error`; no such type exists, and the new order is the correct one for it.
- **Note:** P6 found the same change.
- **Delta:** -3 (`74b09d0`).

Before:

```go
func validationDetails(err error) []validationDetail {
	switch wrapper := err.(type) {
	case nil:
		return nil
	case interface{ Unwrap() error }:
		if details := fieldErrorDetails(err); details != nil {
			return details
		}
		return validationDetails(wrapper.Unwrap())
	case interface{ Unwrap() []error }:
		for _, inner := range wrapper.Unwrap() {
			if details := validationDetails(inner); details != nil {
				return details
			}
		}
		return nil
	}
	return fieldErrorDetails(err)
}
```

After:

```go
func validationDetails(err error) []validationDetail {
	if details := fieldErrorDetails(err); details != nil {
		return details
	}
	switch wrapper := err.(type) {
	case interface{ Unwrap() error }:
		return validationDetails(wrapper.Unwrap())
	case interface{ Unwrap() []error }:
		for _, inner := range wrapper.Unwrap() {
			if details := validationDetails(inner); details != nil {
				return details
			}
		}
	}
	return nil
}
```

## Rejected

### K6. Fold the `Disabled` check into the enablement switch

Status: rejected
Class: b (needs decision)
Note: The early return is clearer than an empty `case`, which saves 2 lines (`6c26ff2`).

```go
// Current code, kept
if c.Disabled {
	return s
}
switch {
case c.WriteToken == "":
	// ...

// Rejected proposal
switch {
case c.Disabled:
case c.WriteToken == "":
	// ...
```

### E10. Remove the redundant result assignments in the panic recovers of mask callbacks

Status: rejected
Class: b (needs decision)
Note: `isKept = false` in `callMaskLogRecord` (part of `df61d30`) and `masked = nil` in `callMaskCallback` (`internal/body_processing.go:89`) are redundant, because a panic leaves the named result at its zero value. Each saves one line, but the assignment states the fallback where the panic is handled.

```go
// Current code, kept
defer func() {
	if p := recover(); p != nil {
		warnOnce("mask-log-record-panic", "Apitally MaskLogRecord callback panicked, log records are dropped", "panic", p)
		isKept = false
	}
}()
return mask(record)
```

## Outside scope

### B1. Fiber v2 routes registered with the same handler slice get the wrong route

Status: fixed
Class: b (needs decision)
Note: `routeKey` now includes the route's path. A middleware route is still told apart by its handler address. A temporary test with the reproduction below passed, and no regression test was added.

This is a bug, not a simplification. It is recorded here because the W reviewer found it.

- **Where:** `fiber-v2/middleware.go:47-66` (`routeKey`).
- **Problem:** the route lookup is keyed by the method and the address of the route's first handler. Fiber v2's `register` stores the variadic handler slice as given. Two routes registered by spreading the same slice therefore share `&Handlers[0]`, and both resolve to whichever path `GetRoutes` lists last. Fiber v3 is not affected, because its `Get(path, handler, handlers...)` always builds a new slice.
- **Reproduction:** on `a522bf1`, with the fiber-v2 test helpers, the request to `/a` is recorded with route `/b`:

  ```go
  handlers := []fiber.Handler{func(c *fiber.Ctx) error { return c.SendString("ok") }}
  app.Get("/a", handlers...)
  app.Get("/b", handlers...)
  send(t, app, http.MethodGet, "/a", nil) // http.route is "/b"
  ```

- **Decision needed:** whether this registration pattern is common enough to fix, or to document as a known limitation.
