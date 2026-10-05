package detection

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Every scenario gets its own globals, including the one-time delegate wiring.
func TestScenarios(t *testing.T) {
	scenarios := map[string]func(*testing.T){
		"unset":                    func(t *testing.T) { detectionMatrix(t, "unset") },
		"sdk":                      func(t *testing.T) { detectionMatrix(t, "sdk") },
		"foreign-recording":        func(t *testing.T) { detectionMatrix(t, "foreign-recording") },
		"explicit-noop":            func(t *testing.T) { detectionMatrix(t, "explicit-noop") },
		"foreign-nonrecording":     func(t *testing.T) { detectionMatrix(t, "foreign-nonrecording") },
		"probe-without-end":        probeWithoutEnd,
		"unset-with-valid-parent":  unsetWithValidParent,
		"cached-tracer-delegation": cachedTracerDelegation,
		"propagator-unset":         propagatorUnset,
		"propagator-empty":         func(t *testing.T) { configuredPropagator(t, "empty") },
		"propagator-w3c":           func(t *testing.T) { configuredPropagator(t, "w3c") },
		"propagator-baggage":       func(t *testing.T) { configuredPropagator(t, "baggage") },
		"processor-registration":   processorRegistration,
		"later-replacement":        laterReplacement,
		"restored-default-handle":  restoredDefaultHandle,
	}
	if scenario := os.Getenv("APITALLY_PROVIDER_POC_SCENARIO"); scenario != "" {
		run, ok := scenarios[scenario]
		if !ok {
			t.Fatalf("unknown scenario %q", scenario)
		}
		run(t)
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for name := range scenarios {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(binary, "-test.run=^TestScenarios$", "-test.v", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "APITALLY_PROVIDER_POC_SCENARIO="+name)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, output)
			}
			t.Logf("%s", output)
		})
	}
}

func detectionMatrix(t *testing.T, scenario string) {
	user, exported := newProvider(t)
	userStarts := &processor{}
	user.RegisterSpanProcessor(userStarts)
	switch scenario {
	case "sdk":
		otel.SetTracerProvider(user)
	case "foreign-recording":
		otel.SetTracerProvider(&foreignProvider{TracerProvider: user})
	case "explicit-noop":
		otel.SetTracerProvider(noop.NewTracerProvider())
	case "foreign-nonrecording":
		otel.SetTracerProvider(&foreignProvider{TracerProvider: noop.NewTracerProvider()})
	}
	global := otel.GetTracerProvider()
	gotProbe := classifyWithProbe(global)
	wantProbe := "unset"
	if scenario == "sdk" {
		wantProbe = "sdk"
	} else if scenario == "foreign-recording" {
		wantProbe = "foreign"
	}
	equal(t, gotProbe, wantProbe)
	wantExports := 0
	if scenario == "foreign-recording" {
		wantExports = 1
	}
	equal(t, len(exported.GetSpans()), wantExports)
	equal(t, userStarts.startCount(), wantExports)

	gotType := classifyWithType(global)
	wantType := "foreign"
	if scenario == "unset" || scenario == "sdk" {
		wantType = scenario
	}
	equal(t, gotType, wantType)
	apitallyProcessor := &processor{}
	var selected *sdktrace.TracerProvider
	switch gotType {
	case "sdk":
		selected = global.(*sdktrace.TracerProvider)
		selected.RegisterSpanProcessor(apitallyProcessor)
	case "unset":
		selected, _ = newProvider(t)
		selected.RegisterSpanProcessor(apitallyProcessor)
		otel.SetTracerProvider(selected)
		if isInternalDefault(otel.GetTextMapPropagator(), "textMapPropagator") {
			otel.SetTextMapPropagator(w3cPropagator())
		}
	case "foreign":
		selected, _ = newProvider(t)
		selected.RegisterSpanProcessor(apitallyProcessor)
	}
	if gotType == "foreign" {
		equal(t, otel.GetTracerProvider() == global, true)
	} else {
		equal(t, otel.GetTracerProvider() == selected, true)
	}
	equal(t, len(exported.GetSpans()), wantExports)
	ctx, server := selected.Tracer("apitally").Start(context.Background(), "SERVER", trace.WithSpanKind(trace.SpanKindServer))
	_, child := otel.Tracer("application").Start(ctx, "child")
	child.End()
	server.End()
	wantApitallySpans := 1
	if gotType != "foreign" {
		wantApitallySpans = 2
	}
	equal(t, apitallyProcessor.startCount(), wantApitallySpans)
	equal(t, apitallyProcessor.endCount(), wantApitallySpans)
	if scenario == "foreign-recording" {
		equal(t, len(exported.GetSpans()), 2)
		equal(t, exported.GetSpans()[1].Parent.SpanID(), server.SpanContext().SpanID())
	}
	t.Logf("global=%T probe=%s type=%s probe_user_starts=%d probe_user_exports=%d selected=%s", global, gotProbe, gotType, wantExports, wantExports, gotType)
}

func probeWithoutEnd(t *testing.T) {
	user, exported := newProvider(t)
	counted := &processor{}
	user.RegisterSpanProcessor(counted)
	otel.SetTracerProvider(&foreignProvider{TracerProvider: user})
	_, probe := otel.Tracer("apitally.probe").Start(context.Background(), "probe")
	equal(t, probe.IsRecording(), true)
	equal(t, probe.SpanContext().IsValid(), true)
	equal(t, counted.startCount(), 1)
	equal(t, counted.endCount(), 0)
	equal(t, len(exported.GetSpans()), 0)
	if err := user.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, len(exported.GetSpans()), 0)
	if err := user.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	equal(t, counted.endCount(), 0)
	t.Log("unended probe: OnStart=1 OnEnd=0 exports=0 after ForceFlush; Shutdown does not call OnEnd; not side-effect-free")
}

func unsetWithValidParent(t *testing.T) {
	user, _ := newProvider(t)
	ctx, parent := user.Tracer("private").Start(context.Background(), "parent")
	defer parent.End()
	_, probe := otel.Tracer("apitally.probe").Start(ctx, "probe")
	defer probe.End()
	equal(t, probe.IsRecording(), false)
	equal(t, probe.SpanContext().IsValid(), true)
	equal(t, probe.SpanContext().Equal(parent.SpanContext()), true)
	equal(t, classifyWithProbe(otel.GetTracerProvider()), "unset")
	t.Log("unset global inherits valid parent context; probe must use context.Background(), not request context")
}

func cachedTracerDelegation(t *testing.T) {
	cachedProvider := otel.GetTracerProvider()
	cachedTracer := otel.Tracer("library", trace.WithInstrumentationVersion("1.0"))
	_, before := cachedTracer.Start(context.Background(), "before-activation")
	equal(t, before.SpanContext().IsValid(), false)
	owned, exported := newProvider(t)
	otel.SetTracerProvider(owned)
	equal(t, isInternalDefault(otel.GetTracerProvider(), "tracerProvider"), false)
	equal(t, isInternalDefault(cachedProvider, "tracerProvider"), true)
	ctx, server := owned.Tracer("apitally").Start(context.Background(), "SERVER", trace.WithSpanKind(trace.SpanKindServer))
	_, child := cachedTracer.Start(ctx, "cached-child")
	_, other := cachedProvider.Tracer("late-library").Start(ctx, "cached-provider-child")
	equal(t, child.IsRecording(), true)
	equal(t, child.SpanContext().TraceID(), server.SpanContext().TraceID())
	child.End()
	other.End()
	server.End()
	before.End()
	spans := exported.GetSpans()
	equal(t, len(spans), 3)
	equal(t, spans[0].Parent.SpanID(), server.SpanContext().SpanID())
	equal(t, spans[1].Parent.SpanID(), server.SpanContext().SpanID())
	equal(t, spans[0].InstrumentationScope.Name, "library")
	equal(t, spans[0].InstrumentationScope.Version, "1.0")
	equal(t, before.IsRecording(), false)
	t.Log("cached tracer and provider delegate: 2 correctly parented children + SERVER exported; old span remains noop")
}

func propagatorUnset(t *testing.T) {
	cached := otel.GetTextMapPropagator()
	equal(t, isInternalDefault(cached, "textMapPropagator"), true)
	equal(t, len(cached.Fields()), 0)
	owned, _ := newProvider(t)
	otel.SetTracerProvider(owned)
	ctx, server := owned.Tracer("apitally").Start(context.Background(), "SERVER")
	defer server.End()
	member, err := baggage.NewMember("tenant", "example")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)
	before := propagation.MapCarrier{}
	cached.Inject(ctx, before)
	equal(t, len(before), 0)
	otel.SetTextMapPropagator(w3cPropagator())
	equal(t, isInternalDefault(otel.GetTextMapPropagator(), "textMapPropagator"), false)
	for _, propagator := range []propagation.TextMapPropagator{cached, otel.GetTextMapPropagator()} {
		carrier := propagation.MapCarrier{}
		propagator.Inject(ctx, carrier)
		if carrier.Get("traceparent") == "" {
			t.Fatal("missing traceparent")
		}
		equal(t, carrier.Get("baggage"), "tenant=example")
		extracted := propagator.Extract(context.Background(), carrier)
		equal(t, trace.SpanContextFromContext(extracted).TraceID(), server.SpanContext().TraceID())
		equal(t, trace.SpanContextFromContext(extracted).SpanID(), server.SpanContext().SpanID())
		equal(t, trace.SpanContextFromContext(extracted).IsRemote(), true)
		equal(t, baggage.FromContext(extracted).Member("tenant").Value(), "example")
		equal(t, len(propagator.Fields()), 3)
	}
	t.Logf("default=%T fields_before=0 fields_after=3; cached + current inject traceparent/baggage and extract remote parent", cached)
}

func configuredPropagator(t *testing.T, kind string) {
	var configured propagation.TextMapPropagator
	switch kind {
	case "empty":
		configured = propagation.NewCompositeTextMapPropagator()
	case "w3c":
		configured = w3cPropagator()
	case "baggage":
		configured = propagation.Baggage{}
	}
	cached := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(configured)
	global := otel.GetTextMapPropagator()
	equal(t, isInternalDefault(global, "textMapPropagator"), false)
	wantFields := 3
	if kind == "empty" {
		wantFields = 0
	} else if kind == "baggage" {
		wantFields = 1
	}
	equal(t, len(global.Fields()), wantFields)
	equal(t, len(cached.Fields()), wantFields)
	// Simulate installing our provider without replacing a configured propagator.
	owned, _ := newProvider(t)
	otel.SetTracerProvider(owned)
	if isInternalDefault(global, "textMapPropagator") {
		otel.SetTextMapPropagator(w3cPropagator())
	}
	equal(t, reflect.TypeOf(otel.GetTextMapPropagator()), reflect.TypeOf(configured))
	t.Logf("explicit %s: type=%T fields=%d preserved; Fields alone cannot identify unset", kind, global, wantFields)
}

func processorRegistration(t *testing.T) {
	user, exported := newProvider(t)
	otel.SetTracerProvider(user)
	tracer := otel.Tracer("user")
	ctx, old := tracer.Start(context.Background(), "in-flight")
	added := &processor{}
	user.RegisterSpanProcessor(added)
	type contextKey struct{}
	parentContext := context.WithValue(ctx, contextKey{}, "request-value")
	_, child := tracer.Start(parentContext, "child")
	child.End()
	old.End()
	equal(t, added.startCount(), 1)
	equal(t, added.endCount(), 2)
	added.mu.Lock()
	equal(t, added.starts[0].parent.Equal(old.SpanContext()), true)
	equal(t, added.starts[0].ctx.Value(contextKey{}), any("request-value"))
	equal(t, added.ends[0].Name(), "child")
	equal(t, added.ends[1].Name(), "in-flight")
	added.mu.Unlock()
	equal(t, len(exported.GetSpans()), 2)

	// All workers hold a live span across registration, then keep producing spans.
	const workers, perWorker, registrations = 8, 500, 16
	ready := make(chan struct{}, workers)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, span := tracer.Start(context.Background(), "held")
			ready <- struct{}{}
			<-release
			span.End()
			for range perWorker {
				_, span := tracer.Start(context.Background(), "traffic")
				span.End()
			}
		}()
	}
	for range workers {
		<-ready
	}
	first := &processor{}
	user.RegisterSpanProcessor(first)
	close(release)
	processors := []*processor{first}
	for range registrations - 1 {
		p := &processor{}
		user.RegisterSpanProcessor(p)
		processors = append(processors, p)
	}
	wg.Wait()
	equal(t, first.startCount(), workers*perWorker)
	equal(t, first.endCount(), workers*(perWorker+1))
	equal(t, len(exported.GetSpans()), 2+workers*(perWorker+1))
	for _, p := range processors {
		if p.endCount() < p.startCount() {
			t.Fatalf("ends=%d < starts=%d", p.endCount(), p.startCount())
		}
	}
	t.Logf("in-flight: OnStart=0 OnEnd=1; child OnStart receives parent span + request value; %d traffic spans, %d registrations", workers*(perWorker+1), registrations)
}

func laterReplacement(t *testing.T) {
	cachedProvider := otel.GetTracerProvider()
	cachedTracer := otel.Tracer("pre-activation")
	cachedPropagator := otel.GetTextMapPropagator()
	owned, ownedExports := newProvider(t)
	otel.SetTracerProvider(owned)
	otel.SetTextMapPropagator(w3cPropagator())
	activationTracer := otel.Tracer("at-activation")
	user, userExports := newProvider(t)
	otel.SetTracerProvider(user)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	equal(t, otel.GetTracerProvider() == user, true)
	equal(t, len(otel.GetTextMapPropagator().Fields()), 0)
	equal(t, len(cachedPropagator.Fields()), 3)
	ctx, server := owned.Tracer("apitally").Start(context.Background(), "SERVER", trace.WithSpanKind(trace.SpanKindServer))
	for _, tracer := range []trace.Tracer{cachedTracer, activationTracer, cachedProvider.Tracer("cached-provider")} {
		_, span := tracer.Start(ctx, "owned-child")
		span.End()
	}
	_, late := otel.Tracer("after-replacement").Start(ctx, "user-child")
	late.End()
	server.End()
	equal(t, len(ownedExports.GetSpans()), 4)
	equal(t, len(userExports.GetSpans()), 1)
	equal(t, userExports.GetSpans()[0].Parent.SpanID(), server.SpanContext().SpanID())
	equal(t, userExports.GetSpans()[0].SpanContext.TraceID(), server.SpanContext().TraceID())
	t.Log("later Set wins for new global tracers; cached default/activation tracers stay with first provider; exports owned=4 user=1; trace parent preserved across providers")
	t.Log("later propagator Set wins for new getters; cached default propagator stays with first W3C delegate")
}

func restoredDefaultHandle(t *testing.T) {
	cached := otel.GetTracerProvider()
	user, exported := newProvider(t)
	otel.SetTracerProvider(user)
	otel.SetTracerProvider(cached)
	equal(t, classifyWithType(otel.GetTracerProvider()), "unset")
	equal(t, classifyWithProbe(otel.GetTracerProvider()), "foreign")
	equal(t, len(exported.GetSpans()), 1)
	t.Log("restoring cached default handle keeps internal type but delegates to user SDK; dynamic type is not a public is-configured API")
}

func classifyWithProbe(provider trace.TracerProvider) string {
	if _, ok := provider.(*sdktrace.TracerProvider); ok {
		return "sdk"
	}
	_, span := provider.Tracer("apitally.probe").Start(context.Background(), "probe")
	defer span.End()
	if !span.SpanContext().IsValid() {
		return "unset"
	}
	return "foreign"
}

func classifyWithType(provider trace.TracerProvider) string {
	if _, ok := provider.(*sdktrace.TracerProvider); ok {
		return "sdk"
	}
	if isInternalDefault(provider, "tracerProvider") {
		return "unset"
	}
	return "foreign"
}

// This uses private implementation names, not a supported OTel detection API.
func isInternalDefault(value any, name string) bool {
	typ := reflect.TypeOf(value)
	return typ != nil && typ.Kind() == reflect.Pointer &&
		typ.Elem().PkgPath() == "go.opentelemetry.io/otel/internal/global" && typ.Elem().Name() == name
}

func w3cPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

func newProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return provider, exporter
}

type foreignProvider struct {
	trace.TracerProvider
}

type startEvent struct {
	ctx    context.Context
	parent trace.SpanContext
}

type processor struct {
	mu     sync.Mutex
	starts []startEvent
	ends   []sdktrace.ReadOnlySpan
}

func (p *processor) OnStart(ctx context.Context, _ sdktrace.ReadWriteSpan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, startEvent{ctx: ctx, parent: trace.SpanContextFromContext(ctx)})
}

func (p *processor) OnEnd(span sdktrace.ReadOnlySpan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ends = append(p.ends, span)
}

func (*processor) Shutdown(context.Context) error   { return nil }
func (*processor) ForceFlush(context.Context) error { return nil }

func (p *processor) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.starts)
}

func (p *processor) endCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ends)
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
