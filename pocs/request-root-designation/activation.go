package rootpoc

import (
	"regexp"
	"sync"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const scopeName = "github.com/apitally/apitally-go/pocs/request-root-designation"

type mode string

const (
	modeAttached mode = "attached"
	modeOwned    mode = "owned"
	modePrivate  mode = "private"
)

var initialProvider = otel.GetTracerProvider()

type Config struct {
	ExcludePaths    []*regexp.Regexp
	SampleRate      float64
	SampleOnRequest func(sdktrace.ReadOnlySpan) (float64, bool)
}

type Release struct {
	Spans     []sdktrace.ReadOnlySpan
	LastEvent string
}

type Apitally struct {
	cfg      Config
	export   func(Release)
	once     sync.Once
	mode     mode
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	requests *processor
}

func New(cfg Config, export func(Release)) *Apitally {
	return &Apitally{cfg: cfg, export: export, requests: &processor{entries: map[trace.SpanID]*request{}}}
}

func (a *Apitally) activate() {
	a.once.Do(func() {
		global := otel.GetTracerProvider()
		if sdk, ok := global.(*sdktrace.TracerProvider); ok {
			a.mode, a.provider = modeAttached, sdk
			sdk.RegisterSpanProcessor(a.requests)
		} else {
			a.provider = sdktrace.NewTracerProvider(
				sdktrace.WithSampler(fallbackSampler{rate: a.cfg.SampleRate, skipRate: a.cfg.SampleOnRequest != nil}),
				sdktrace.WithSpanProcessor(a.requests),
			)
			a.mode = modePrivate
			if global == initialProvider {
				a.mode = modeOwned
				otel.SetTracerProvider(a.provider)
			}
		}
		a.tracer = a.provider.Tracer(scopeName)
	})
}

type fallbackSampler struct {
	rate     float64
	skipRate bool
}

func (s fallbackSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	parent := trace.SpanContextFromContext(p.ParentContext)
	record := false
	switch {
	case p.Kind == trace.SpanKindServer:
		record = (parent.IsRemote() && parent.IsSampled()) || s.skipRate || keepTraceID(p.TraceID, s.rate)
	case parent.IsValid() && !parent.IsRemote():
		record = parent.IsSampled()
	}
	decision := sdktrace.Drop
	if record {
		decision = sdktrace.RecordAndSample
	}
	return sdktrace.SamplingResult{Decision: decision, Tracestate: parent.TraceState()}
}

func (fallbackSampler) Description() string { return "ApitallyFallbackSampler" }
