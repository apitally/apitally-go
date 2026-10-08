package internal

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const maxSpanAttributeValueLength = 65_536

// The globals are captured before main runs. At activation, a global identical
// to its captured value has not been set by the application, because
// otel.SetTracerProvider and otel.SetTextMapPropagator replace the value.
var (
	initialTracerProvider = otel.GetTracerProvider()
	initialPropagator     = otel.GetTextMapPropagator()
	defaultPropagator     = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
)

// setUpTracerProvider attaches the span processor to a global SDK tracer
// provider. Otherwise it creates Apitally's own provider, registered globally
// when the global is unset, so application spans become request descendants,
// or kept private when the application set a provider of another type.
func (r *sdkRuntime) setUpTracerProvider() {
	global := otel.GetTracerProvider()
	if provider, ok := global.(*sdktrace.TracerProvider); ok {
		provider.RegisterSpanProcessor(r.spanProcessor)
		r.provider = provider
	} else {
		limits := sdktrace.NewSpanLimits()
		limits.AttributeValueLengthLimit = maxSpanAttributeValueLength
		r.provider = sdktrace.NewTracerProvider(
			sdktrace.WithSampler(fallbackSampler{rate: r.settings.config.SampleRate, isRateTestSkipped: r.settings.config.SampleOnRequest != nil}),
			sdktrace.WithResource(r.resource),
			sdktrace.WithRawSpanLimits(limits),
			sdktrace.WithSpanProcessor(r.spanProcessor),
		)
		r.isProviderOwned = true
		if global == initialTracerProvider {
			otel.SetTracerProvider(r.provider)
		} else {
			r.isProviderPrivate = true
			logWarn("The global OpenTelemetry tracer provider is not an SDK tracer provider, so Apitally captures requests without their descendant spans. Register a go.opentelemetry.io/otel/sdk/trace TracerProvider with otel.SetTracerProvider before the first request to capture them.")
		}
	}
	if otel.GetTextMapPropagator() == initialPropagator {
		otel.SetTextMapPropagator(defaultPropagator)
	}
	r.tracer = r.provider.Tracer(r.framework.ScopeName, trace.WithInstrumentationVersion(sdkVersion))
}

// tearDownTracerProvider shuts down Apitally's own provider and detaches the
// span processor from an application's provider, which stays running.
func (r *sdkRuntime) tearDownTracerProvider(ctx context.Context) {
	if r.isProviderOwned {
		_ = r.provider.Shutdown(ctx)
	} else {
		r.provider.UnregisterSpanProcessor(r.spanProcessor)
	}
}

// propagator returns the global propagator, which Apitally registers only
// while it is unset, so an application-set propagator applies as-is.
func propagator() propagation.TextMapPropagator {
	if p := otel.GetTextMapPropagator(); p != initialPropagator {
		return p
	}
	return defaultPropagator
}

// restoreGlobalsForTest restores the OTel globals captured at package
// initialization.
func restoreGlobalsForTest() {
	if otel.GetTracerProvider() != initialTracerProvider {
		otel.SetTracerProvider(initialTracerProvider)
	}
	if otel.GetTextMapPropagator() != initialPropagator {
		otel.SetTextMapPropagator(initialPropagator)
	}
}
