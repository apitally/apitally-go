package internal

import (
	"encoding/binary"
	"math"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// isTraceIDSampled keeps a trace when the low 64 bits of its ID fall under
// round(rate * 2^64), the ratio-sampler convention. Both sampling stages test
// the same bits, so the combined rate is the lower of the two rates, and
// services sampling at the same rate keep the same traces.
func isTraceIDSampled(id trace.TraceID, rate float64) bool {
	if rate >= 1 {
		return true
	}
	if !(rate > 0) {
		return false
	}
	return binary.BigEndian.Uint64(id[8:]) < uint64(math.Round(math.Ldexp(rate, 64)))
}

func (r *sdkRuntime) shouldKeepAtRequestStage(span sdktrace.ReadOnlySpan) bool {
	rate := r.settings.config.SampleRate
	if callback := r.settings.config.SampleOnRequest; callback != nil {
		if callbackRate, ok := callSampleCallback("SampleOnRequest", callback, span); ok {
			rate = callbackRate
		}
	}
	return isTraceIDSampled(span.SpanContext().TraceID(), rate)
}

// shouldKeepAtResponseStage keeps the request-stage decision when the
// callback abstains.
func (r *sdkRuntime) shouldKeepAtResponseStage(span sdktrace.ReadOnlySpan) bool {
	callback := r.settings.config.SampleOnResponse
	if callback == nil {
		return true
	}
	rate, ok := callSampleCallback("SampleOnResponse", callback, span)
	return !ok || isTraceIDSampled(span.SpanContext().TraceID(), rate)
}

// callSampleCallback fails open: a panic or an invalid rate warns and keeps
// the request.
func callSampleCallback(option string, callback func(sdktrace.ReadOnlySpan) (float64, bool), span sdktrace.ReadOnlySpan) (rate float64, ok bool) {
	defer func() {
		if p := recover(); p != nil {
			warnOnce("sample-callback-panic-"+option, "Apitally "+option+" callback panicked, requests are kept", "panic", p)
			rate, ok = 1, true
		}
	}()
	rate, ok = callback(span)
	if ok && !(rate >= 0 && rate <= 1) {
		warnOnce("sample-callback-invalid-"+option, "Apitally "+option+" callback returned a rate outside [0, 1], requests are kept", "rate", rate)
		return 1, true
	}
	return rate, ok
}

// fallbackSampler is used when Apitally owns the tracer provider. It records
// SERVER spans subject to the request-stage rate test and children of
// recorded local parents, and nothing else.
type fallbackSampler struct {
	rate float64
	// The SampleOnRequest callback can raise the rate, so it needs every
	// request recorded.
	isRateTestSkipped bool
}

func (s fallbackSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	parent := trace.SpanContextFromContext(p.ParentContext)
	isRecorded := false
	switch {
	case p.Kind == trace.SpanKindServer:
		isRecorded = (parent.IsRemote() && parent.IsSampled()) || s.isRateTestSkipped || isTraceIDSampled(p.TraceID, s.rate)
	case parent.IsValid() && !parent.IsRemote():
		isRecorded = parent.IsSampled()
	}
	decision := sdktrace.Drop
	if isRecorded {
		decision = sdktrace.RecordAndSample
	}
	return sdktrace.SamplingResult{Decision: decision, Tracestate: parent.TraceState()}
}

func (fallbackSampler) Description() string { return "ApitallyFallbackSampler" }
