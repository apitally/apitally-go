package internal

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const maxBufferedSpansPerRequest = 1000

var (
	defaultExcludedPathPattern      = regexp.MustCompile(`(?i)/_?healthz?/?$|/_?health[-_]?checks?/?$|/_?heart[-_]?beats?/?$|/ping/?$|/ready/?$|/live/?$|/favicon(?:-[\w-]+)?\.(ico|png|svg)$|/apple-touch-icon(?:-[\w-]+)?\.png$|/robots\.txt$|/sitemap\.xml$|/manifest\.json$|/site\.webmanifest$|/service-worker\.js$|/sw\.js$|/\.well-known/`)
	defaultExcludedUserAgentPattern = regexp.MustCompile(`(?i)health[-_ ]?check|microsoft-azure-application-lb|googlehc|kube-probe`)
)

// RequestInfo holds request data copied from the framework at middleware
// entry.
type RequestInfo struct {
	Method string
	Scheme string
	Host   string
	Path   string
	Query  string
	Header http.Header
	// ContentLength is the declared request body size, or -1 when unknown.
	ContentLength int64
}

// TransportResult holds the response data observed when transport
// observation ends.
type TransportResult struct {
	Route         string
	StatusCode    int
	ClientAddress string
	// RequestBodySize and ResponseBodySize are -1 when unknown.
	RequestBodySize  int64
	ResponseBodySize int64
	ResponseHeader   http.Header
	// RequestBody and ResponseBody are complete captured bodies, the
	// too-large marker, or nil.
	RequestBody  []byte
	ResponseBody []byte
}

// RequestState is the per-request state, stored in the request context.
type RequestState struct {
	runtime       *sdkRuntime
	info          RequestInfo
	startTime     time.Time
	span          trace.Span
	isSpanCreated bool
	// requestAttributes are Apitally's attributes for a reused span, which
	// only its export copy carries.
	requestAttributes []attribute.KeyValue
	// isMonitored is set when the request's trace and log detail is kept
	// until release.
	isMonitored bool

	members []trace.SpanID // guarded by requestRegistry.mu

	mu                  sync.Mutex
	descendants         []sdktrace.ReadOnlySpan
	root                sdktrace.ReadOnlySpan
	transportAttributes []attribute.KeyValue
	transportEnd        time.Time
	payload             *payloadStash
	isObserved          bool
	isReleased          bool
	consumer            *requestConsumer
	capturedError       *capturedError
	// channelError is the first error from the framework's error channel.
	channelError      error
	validationDetails []validationDetail
}

type requestStateKey struct{}

// BeginRequest selects the request's SERVER span, applies exclusions and
// request sampling, and stores the request state in the returned context. It
// returns a nil state when Apitally is inactive.
func BeginRequest(ctx context.Context, info RequestInfo) (state *RequestState, requestCtx context.Context) {
	r := currentRuntime.Load()
	if r == nil || !r.active.Load() {
		return nil, ctx
	}
	defer func() {
		if p := recover(); p != nil {
			logPanic("request start", p)
			state, requestCtx = nil, ctx
		}
	}()
	state = &RequestState{runtime: r, info: info, startTime: time.Now()}
	attrs := requestAttributes(&info)
	ctx, state.span, state.isSpanCreated = r.startServerSpan(ctx, &info, attrs)
	if !state.isSpanCreated {
		state.requestAttributes = attrs
	}
	if live, ok := state.span.(sdktrace.ReadOnlySpan); ok && state.span.IsRecording() && !r.isExcluded(&info) &&
		r.shouldKeepAtRequestStage(&exportSpan{ReadOnlySpan: live, attributes: mergeAttributes(live.Attributes(), attrs)}) {
		state.isMonitored = r.registry.register(state)
	}
	return state, context.WithValue(ctx, requestStateKey{}, state)
}

// FinishObservation records the response data and marks transport
// observation complete. It ends a SERVER span that Apitally started.
func (s *RequestState) FinishObservation(result TransportResult) {
	if s == nil {
		return
	}
	defer recoverAndLogPanic("request observation")
	end := time.Now()
	r := s.runtime
	s.mu.Lock()
	consumer := s.consumer
	s.mu.Unlock()
	attrs := transportAttributes(&result)
	if consumer != nil {
		attrs = append(attrs, attribute.String("apitally.consumer.identifier", toValidUTF8(consumer.identifier)))
	}
	if !isWebSocketUpgrade(s.info.Header) {
		s.recordRequestData(&result, end, consumer)
	}
	if s.isMonitored {
		payload := &payloadStash{
			requestBody:      result.RequestBody,
			responseBody:     result.ResponseBody,
			requestEncoding:  s.info.Header.Get("Content-Encoding"),
			responseEncoding: result.ResponseHeader.Get("Content-Encoding"),
		}
		if r.settings.config.CaptureRequestHeaders {
			payload.requestHeader = s.info.Header.Clone()
		}
		if r.settings.config.CaptureResponseHeaders {
			payload.responseHeader = result.ResponseHeader.Clone()
		}
		s.mu.Lock()
		s.transportAttributes, s.transportEnd, s.payload, s.isObserved = attrs, end, payload, true
		isReleasable := s.claimReleaseLocked()
		s.mu.Unlock()
		if isReleasable {
			s.release()
		}
	}
	if s.isSpanCreated {
		finishServerSpan(s.span, s.info.Method, result.Route, result.StatusCode, attrs, end)
	}
}

// recordRequestData records the consumer update, and for routed requests the
// metrics and errors, independently of exclusions and sampling.
func (s *RequestState) recordRequestData(result *TransportResult, end time.Time, consumer *requestConsumer) {
	r := s.runtime
	consumerIdentifier := ""
	if consumer != nil {
		consumerIdentifier = consumer.identifier
		if consumer.hasMetadata() && r.consumers.isChanged(consumer) {
			r.logs.emitEvent(consumerUpdateEventName, consumerUpdateEventBody(consumer))
		}
	}
	method := s.info.Method
	if result.Route == "" || method == http.MethodOptions {
		return
	}
	key := requestMetricKey{method: method, route: result.Route, statusCode: result.StatusCode, scheme: s.info.Scheme, consumer: consumerIdentifier}
	r.metrics.recordRequest(key, end.Sub(s.startTime), result.RequestBodySize, result.ResponseBodySize)
	if !isValidErrorMethod(method) {
		return
	}
	s.mu.Lock()
	captured, channelError, details := s.capturedError, s.channelError, s.validationDetails
	s.mu.Unlock()
	path := truncateString(toValidUTF8(result.Route), maxErrorPath)
	consumerIdentifier = toValidUTF8(consumerIdentifier)
	if result.StatusCode == http.StatusBadRequest || result.StatusCode == http.StatusUnprocessableEntity {
		details = append(slices.Clone(details), validationDetails(channelError)...)
	}
	for _, detail := range details {
		r.validationErrors.add(validationErrorKey{method: method, path: path, validationDetail: detail}, consumerIdentifier)
	}
	if result.StatusCode == http.StatusInternalServerError && captured != nil {
		r.serverErrors.add(serverErrorKey{method: method, path: path, typeName: captured.typeName, message: captured.message, stacktrace: captured.stacktrace}, consumerIdentifier)
	}
}

// spanEnded buffers an ended request member. Spans ending after release are
// dropped.
func (s *RequestState) spanEnded(span sdktrace.ReadOnlySpan) {
	s.mu.Lock()
	isReleasable := false
	switch {
	case s.isReleased:
	case span.SpanContext().SpanID() == s.span.SpanContext().SpanID():
		s.root = span
		isReleasable = s.claimReleaseLocked()
	case len(s.descendants) < maxBufferedSpansPerRequest:
		s.descendants = append(s.descendants, span)
	}
	s.mu.Unlock()
	if isReleasable {
		s.release()
	}
}

// claimReleaseLocked claims the release once both transport observation and
// the SERVER span end have happened, in either order. The buffers are not
// modified after the claim.
func (s *RequestState) claimReleaseLocked() bool {
	if s.isReleased || !s.isObserved || s.root == nil {
		return false
	}
	s.isReleased = true
	return true
}

// release exports the request's descendants and then its SERVER span, unless
// response sampling drops the request.
func (s *RequestState) release() {
	r := s.runtime
	r.registry.remove(s)
	if !r.active.Load() {
		return
	}
	defer recoverAndLogPanic("request release")
	root := r.newExportSpan(s, s.root)
	if !r.shouldKeepAtResponseStage(root) {
		return
	}
	for _, span := range s.descendants {
		r.batchProcessor.OnEnd(r.newExportSpan(s, span))
	}
	r.batchProcessor.OnEnd(root)
}

// shouldCaptureRequestBody decides from the request headers alone.
func (s *RequestState) shouldCaptureRequestBody() bool {
	return s != nil && s.isMonitored && s.runtime.settings.config.CaptureRequestBody && isBodyCaptureAllowed(s.info.Header)
}

// shouldCaptureResponseBody decides from the response headers alone.
func (s *RequestState) shouldCaptureResponseBody(header http.Header) bool {
	return s != nil && s.isMonitored && s.runtime.settings.config.CaptureResponseBody && isBodyCaptureAllowed(header)
}

func (r *sdkRuntime) isExcluded(info *RequestInfo) bool {
	return info.Method == http.MethodOptions || isWebSocketUpgrade(info.Header) ||
		defaultExcludedPathPattern.MatchString(info.Path) || matchesAny(r.settings.excludePaths, info.Path) ||
		defaultExcludedUserAgentPattern.MatchString(info.Header.Get("User-Agent"))
}

func isWebSocketUpgrade(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Upgrade")), "websocket")
}

func requestStateFromContext(ctx context.Context) *RequestState {
	state, _ := ctx.Value(requestStateKey{}).(*RequestState)
	return state
}

// requestRegistry maps the span IDs of monitored requests' SERVER spans and
// their descendants to the request state. Lock order: RequestState.mu before
// requestRegistry.mu.
type requestRegistry struct {
	mu       sync.Mutex
	entries  map[trace.SpanID]*RequestState
	isCutOff bool
}

func newRequestRegistry() *requestRegistry {
	return &requestRegistry{entries: map[trace.SpanID]*RequestState{}}
}

// register makes the request's SERVER span a request root. It replaces an
// entry inherited from an enclosing monitored request.
func (g *requestRegistry) register(s *RequestState) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.isCutOff {
		return false
	}
	g.addLocked(s, s.span.SpanContext().SpanID())
	return true
}

// link adds a span to the request of its local parent, if any.
func (g *requestRegistry) link(parent, child trace.SpanID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.entries[parent]; s != nil {
		g.addLocked(s, child)
	}
}

func (g *requestRegistry) lookup(id trace.SpanID) *RequestState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.entries[id]
}

func (g *requestRegistry) isRegistered(id trace.SpanID) bool {
	return g.lookup(id) != nil
}

// remove deletes the entries the request still owns.
func (g *requestRegistry) remove(s *RequestState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range s.members {
		if g.entries[id] == s {
			delete(g.entries, id)
		}
	}
	s.members = nil
}

// cutOff discards all requests not yet released.
func (g *requestRegistry) cutOff() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.isCutOff = true
	clear(g.entries)
}

func (g *requestRegistry) addLocked(s *RequestState, id trace.SpanID) {
	g.entries[id] = s
	s.members = append(s.members, id)
}
