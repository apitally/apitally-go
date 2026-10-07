package rootpoc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

var scenarios = []scenario{
	{"1-no-outer-attached", noOuterAttached},
	{"2a-outer-otelhttp-attached", func(t *testing.T) { outerAttached(t, "nethttp") }},
	{"2b-outer-otelgin-attached", func(t *testing.T) { outerAttached(t, "gin") }},
	{"3a-outer-otelhttp-owned", func(t *testing.T) { outerOwned(t, "nethttp") }},
	{"3b-outer-otelgin-owned", func(t *testing.T) { outerOwned(t, "gin") }},
	{"3c-outer-fiberotel-owned", func(t *testing.T) { outerOwned(t, "fiber") }},
	{"4-outer-otelhttp-private", outerPrivate},
	{"5-inner-instrumentation", innerInstrumentation},
	{"6a-user-sampler-parentbased-never", func(t *testing.T) { userSamplerDrops(t, sdktrace.ParentBased(sdktrace.NeverSample())) }},
	{"6b-user-sampler-ratio-zero", func(t *testing.T) { userSamplerDrops(t, sdktrace.TraceIDRatioBased(0)) }},
	{"6c-user-sampler-record-only", func(t *testing.T) { userSamplerDrops(t, recordOnlySampler{}) }},
	{"7-completion-order", completionOrder},
	{"8-excluded-path", excludedPath},
	{"9-sample-on-request", sampleOnRequest},
	{"10a-concurrency-attached", func(t *testing.T) { concurrency(t, true) }},
	{"10b-concurrency-owned", func(t *testing.T) { concurrency(t, false) }},
	{"11-nested-monitored-request", nestedRequest},
}

func noOuterAttached(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1})
	for _, app := range allApps(t, a, appOptions{}) {
		if code := app.do("/work/1"); code != 200 {
			t.Fatalf("%s: status %d", app.name, code)
		}
		us := takeUser(user).Snapshots()
		rel := requireOneRelease(t, app.name, exp.take())
		if len(us) != 2 || count(us, isServer) != 1 {
			t.Fatalf("%s: user spans %s", app.name, describe(us))
		}
		server, work := serverOf(us), named(us, "work")
		if root(rel).SpanContext().SpanID() != server.SpanContext().SpanID() || work.Parent().SpanID() != server.SpanContext().SpanID() {
			t.Fatalf("%s: root mismatch user=%s apitally=%s", app.name, describe(us), describe(rel.Spans))
		}
		if len(rel.Spans) != 2 || named(rel.Spans, "work") == nil {
			t.Fatalf("%s: apitally spans %s", app.name, describe(rel.Spans))
		}
		t.Logf("%s mode=%s user=%s apitally=%s last=%s", app.name, a.mode, describe(us), describe(rel.Spans), rel.LastEvent)
	}
	requireEmptyMap(t, a)
}

func outerAttached(t *testing.T, framework string) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1})
	var ap app
	scope := scopeOtelhttp
	if framework == "gin" {
		ap, scope = ginApp(a, appOptions{outer: true}), scopeOtelgin
	} else {
		ap = netApp(a, appOptions{outer: true})
	}
	for i := 1; i <= 2; i++ {
		before := a.mode
		ap.do(fmt.Sprintf("/work/%d", i))
		us := takeUser(user).Snapshots()
		rel := requireOneRelease(t, ap.name, exp.take())
		servers := count(us, isServer)
		if servers != 1 {
			t.Fatalf("request %d: want 1 SERVER in user export, got %d: %s", i, servers, describe(us))
		}
		userServer := serverOf(us)
		r := root(rel)
		if r.SpanContext().SpanID() != userServer.SpanContext().SpanID() || r.InstrumentationScope().Name != scope {
			t.Fatalf("request %d: Apitally root %s is not the user's span %s", i, describe(rel.Spans), describe(us))
		}
		if named(rel.Spans, "work") == nil {
			t.Fatalf("request %d: handler span missing from Apitally: %s", i, describe(rel.Spans))
		}
		if _, ok := attr(r.Attributes(), headerAttr); !ok {
			t.Fatalf("request %d: export copy lacks Apitally attributes", i)
		}
		status, _ := attr(r.Attributes(), "http.response.status_code")
		route, _ := attr(r.Attributes(), "http.route")
		if _, ok := attr(userServer.Attributes(), headerAttr); ok {
			t.Fatalf("request %d: user's span carries Apitally attribute %s", i, headerAttr)
		}
		_, userRoute := attr(userServer.Attributes(), "http.route")
		if rel.LastEvent != "root-end" {
			t.Fatalf("request %d: want release on root end, got %s", i, rel.LastEvent)
		}
		t.Logf("request %d: mode before=%q after=%s user=%s apitally=%s last=%s copy(status=%s route=%q header=yes) userSpan(route=%t header=no)",
			i, before, a.mode, describe(us), describe(rel.Spans), rel.LastEvent, status.Emit(), route.Emit(), userRoute)
	}
	requireEmptyMap(t, a)
}

func outerOwned(t *testing.T, framework string) {
	a, exp := newApitally(Config{SampleRate: 1})
	var ap app
	scope := scopeOtelhttp
	switch framework {
	case "gin":
		ap, scope = ginApp(a, appOptions{outer: true}), scopeOtelgin
	case "fiber":
		ap, scope = fiberApp(t, a, appOptions{outer: true}), scopeFiberOtel
	default:
		ap = netApp(a, appOptions{outer: true})
	}
	for i := 1; i <= 3; i++ {
		ap.do(fmt.Sprintf("/work/%d", i))
		rel := requireOneRelease(t, ap.name, exp.take())
		r := root(rel)
		want := scope
		if i == 1 {
			want = scopeName
		}
		if r.InstrumentationScope().Name != want {
			t.Fatalf("request %d: root scope %s, want %s", i, r.InstrumentationScope().Name, want)
		}
		work := named(rel.Spans, "work")
		if work == nil || work.Parent().SpanID() != r.SpanContext().SpanID() {
			t.Fatalf("request %d: handler span not under root: %s", i, describe(rel.Spans))
		}
		t.Logf("request %d: mode=%s apitally=%s rootParentValid=%t last=%s", i, a.mode, describe(rel.Spans), r.Parent().IsValid(), rel.LastEvent)
	}
	requireEmptyMap(t, a)
}

func outerPrivate(t *testing.T) {
	foreignExp := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(&foreignProvider{sdk: sdktrace.NewTracerProvider(sdktrace.WithSyncer(foreignExp))})
	a, exp := newApitally(Config{SampleRate: 1})
	ap := netApp(a, appOptions{outer: true})
	for i := 1; i <= 2; i++ {
		ap.do(fmt.Sprintf("/work/%d", i))
		rel := requireOneRelease(t, ap.name, exp.take())
		fs := takeUser(foreignExp).Snapshots()
		r := root(rel)
		outer, work := serverOf(fs), named(fs, "work")
		if a.mode != modePrivate || len(rel.Spans) != 1 || r.InstrumentationScope().Name != scopeName {
			t.Fatalf("request %d: mode=%s apitally=%s", i, a.mode, describe(rel.Spans))
		}
		if outer == nil || work == nil || len(fs) != 2 || count(fs, func(s sdktrace.ReadOnlySpan) bool { return s.InstrumentationScope().Name == scopeName }) != 0 {
			t.Fatalf("request %d: foreign export changed: %s", i, describe(fs))
		}
		if r.Parent().SpanID() != outer.SpanContext().SpanID() || r.SpanContext().TraceID() != outer.SpanContext().TraceID() {
			t.Fatalf("request %d: Apitally span is not a child of the foreign span", i)
		}
		dangling := work.Parent().SpanID() == r.SpanContext().SpanID()
		if !dangling {
			t.Fatalf("request %d: expected S6 behavior (handler span parented to private span)", i)
		}
		t.Logf("request %d: mode=%s apitally=%s foreign=%s apitallyParent=foreign outer span; S6: foreign 'work' parent=%s (Apitally private span, absent from foreign export)",
			i, a.mode, describe(rel.Spans), describe(fs), work.Parent().SpanID())
	}
	requireEmptyMap(t, a)
}

func innerInstrumentation(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1})
	for _, ap := range allApps(t, a, appOptions{inner: true}) {
		ap.do("/work/1")
		us := takeUser(user).Snapshots()
		rel := requireOneRelease(t, ap.name, exp.take())
		r := root(rel)
		if r.InstrumentationScope().Name != scopeName || len(rel.Spans) != 3 {
			t.Fatalf("%s: apitally=%s", ap.name, describe(rel.Spans))
		}
		var inner, innerUser sdktrace.ReadOnlySpan
		for _, s := range rel.Spans {
			if s.InstrumentationScope().Name != scopeName && s.Name() != "work" {
				inner = s
			}
		}
		for _, s := range us {
			if inner != nil && s.SpanContext().SpanID() == inner.SpanContext().SpanID() {
				innerUser = s
			}
		}
		if inner == nil || inner.SpanKind() != trace.SpanKindInternal || inner.Parent().SpanID() != r.SpanContext().SpanID() {
			t.Fatalf("%s: inner span not linked as INTERNAL: %s", ap.name, describe(rel.Spans))
		}
		if innerUser == nil || innerUser.SpanKind() != trace.SpanKindServer || count(us, isServer) != 2 {
			t.Fatalf("%s: user export changed: %s", ap.name, describe(us))
		}
		if named(rel.Spans, "work").Parent().SpanID() != inner.SpanContext().SpanID() {
			t.Fatalf("%s: handler span not under inner span", ap.name)
		}
		t.Logf("%s: user=%s apitally=%s", ap.name, describe(us), describe(rel.Spans))
	}
	requireEmptyMap(t, a)
}

func userSamplerDrops(t *testing.T, sampler sdktrace.Sampler) {
	user := attachUser(sampler)
	a, exp := newApitally(Config{SampleRate: 1})
	apps := append(allApps(t, a, appOptions{outer: true}), allApps(t, a, appOptions{})...)
	for _, ap := range apps {
		for i := range 3 {
			ap.do(fmt.Sprintf("/work/%d", i))
		}
		ap.do("/panic")
	}
	if rels, us := exp.take(), takeUser(user); len(rels) != 0 || len(us) != 0 {
		t.Fatalf("want no exports, got apitally=%d user=%d", len(rels), len(us))
	}
	requireEmptyMap(t, a)
	t.Logf("sampler=%s mode=%s requests=%d apitallyReleases=0 userSpans=0 mapEntries=0", sampler.Description(), a.mode, len(apps)*4)
}

func completionOrder(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1})
	outer := fiberApp(t, a, appOptions{outer: true})
	plain := fiberApp(t, a, appOptions{})
	for _, path := range []string{"/work/1", "/work/2", "/stream"} {
		outer.do(path)
		us := takeUser(user).Snapshots()
		rel := requireOneRelease(t, outer.name+path, exp.take())
		r := root(rel)
		userServer := serverOf(us)
		if count(us, isServer) != 1 || userServer.SpanContext().SpanID() != r.SpanContext().SpanID() {
			t.Fatalf("%s: root mismatch user=%s apitally=%s", path, describe(us), describe(rel.Spans))
		}
		if rel.LastEvent != "observation" {
			t.Fatalf("%s: want release after observation (user span ended first), got %s", path, rel.LastEvent)
		}
		status, _ := attr(r.Attributes(), "http.response.status_code")
		size, _ := attr(r.Attributes(), "http.response.body.size")
		header, hasHeader := attr(r.Attributes(), headerAttr)
		_, userHeader := attr(userServer.Attributes(), headerAttr)
		if !hasHeader || userHeader || status.AsInt64() != 200 {
			t.Fatalf("%s: copy attrs status=%v header=%v userHeader=%v", path, status.Emit(), hasHeader, userHeader)
		}
		if path == "/stream" {
			sw := named(rel.Spans, "stream-write")
			if sw == nil || sw.Parent().SpanID() != r.SpanContext().SpanID() {
				t.Fatalf("stream-time span missing from Apitally release: %s", describe(rel.Spans))
			}
			if !sw.StartTime().After(userServer.EndTime()) {
				t.Fatalf("stream span did not start after user span end")
			}
		} else if size.AsInt64() != int64(len(body)) {
			t.Fatalf("%s: size %d", path, size.AsInt64())
		}
		t.Logf("fiber+outer %s: last=%s apitally=%s copy(status=%s size=%s header=%s) userSpan(header=%t)",
			path, rel.LastEvent, describe(rel.Spans), status.Emit(), size.Emit(), header.Emit(), userHeader)
	}
	plain.do("/work/1")
	takeUser(user)
	rel := requireOneRelease(t, plain.name, exp.take())
	t.Logf("fiber (created): last=%s apitally=%s", rel.LastEvent, describe(rel.Spans))
	net := netApp(a, appOptions{outer: true})
	net.do("/work/1")
	takeUser(user)
	rel = requireOneRelease(t, net.name, exp.take())
	if rel.LastEvent != "root-end" {
		t.Fatalf("nethttp+outer: want root-end last, got %s", rel.LastEvent)
	}
	t.Logf("nethttp+outer: last=%s (observation completed before outer otelhttp span ended)", rel.LastEvent)
	requireEmptyMap(t, a)
}

func excludedPath(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1, ExcludePaths: []*regexp.Regexp{regexp.MustCompile(`^/healthz$`)}})
	apps := append(allApps(t, a, appOptions{}), allApps(t, a, appOptions{outer: true})...)
	for _, ap := range apps {
		ap.do("/healthz")
		us := takeUser(user).Snapshots()
		if rels := exp.take(); len(rels) != 0 {
			t.Fatalf("%s: excluded request exported to Apitally", ap.name)
		}
		if n := count(us, func(s sdktrace.ReadOnlySpan) bool { return s.InstrumentationScope().Name == scopeName }); n != 0 {
			t.Fatalf("%s: Apitally created a span for an excluded request", ap.name)
		}
		hw := named(us, "healthz-work")
		t.Logf("%s: user=%s healthz-work parentValid=%t", ap.name, describe(us), hw != nil && hw.Parent().IsValid())
	}
	requireEmptyMap(t, a)
}

func sampleOnRequest(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	var mu sync.Mutex
	calls := map[string]int{}
	seen := map[string]string{}
	var current string
	cfg := Config{SampleRate: 1, SampleOnRequest: func(s sdktrace.ReadOnlySpan) (float64, bool) {
		mu.Lock()
		defer mu.Unlock()
		calls[current]++
		raw := s.(*exportCopy).ReadOnlySpan.Attributes()
		seen[current] = fmt.Sprintf("view=[%s] live=[%s]", keys(s.Attributes()), keys(raw))
		if p, _ := attr(s.Attributes(), "url.path"); p.AsString() == "/work/drop" {
			return 0, true
		}
		return 0, false
	}}
	a, exp := newApitally(cfg)
	apps := append(allApps(t, a, appOptions{}), allApps(t, a, appOptions{outer: true})...)
	for _, ap := range apps {
		for _, path := range []string{"/work/1", "/work/drop"} {
			mu.Lock()
			current = ap.name + " " + path
			mu.Unlock()
			ap.do(path)
			us := takeUser(user)
			rels := exp.take()
			if calls[current] != 1 {
				t.Fatalf("%s: SampleOnRequest called %d times", current, calls[current])
			}
			wantReleases := 1
			if path == "/work/drop" {
				wantReleases = 0
			}
			if len(rels) != wantReleases || count(us.Snapshots(), isServer) != 1 {
				t.Fatalf("%s: releases=%d userServers=%d", current, len(rels), count(us.Snapshots(), isServer))
			}
			t.Logf("%s: calls=1 releases=%d %s", current, len(rels), seen[current])
		}
	}
	requireEmptyMap(t, a)
}

func concurrency(t *testing.T, attached bool) {
	user := tracetest.NewInMemoryExporter()
	if attached {
		user = attachUser(sdktrace.AlwaysSample())
	}
	cfg := Config{SampleRate: 1, ExcludePaths: []*regexp.Regexp{regexp.MustCompile(`^/healthz$`)},
		SampleOnRequest: func(s sdktrace.ReadOnlySpan) (float64, bool) {
			if p, _ := attr(s.Attributes(), "url.path"); p.AsString() == "/work/drop" {
				return 0, true
			}
			return 0, false
		}}
	a, exp := newApitally(cfg)
	gate := &lateGate{release: make(chan struct{})}
	apps := append(allApps(t, a, appOptions{late: gate}), allApps(t, a, appOptions{outer: true, late: gate})...)
	const perPath = 20
	paths := []string{"/work/1", "/panic", "/healthz", "/work/drop", "/late"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, ap := range apps {
		for _, path := range paths {
			for range perPath {
				wg.Go(func() {
					<-start
					want := 200
					if path == "/panic" {
						want = 500
					}
					if code := ap.do(path); code != want {
						t.Errorf("%s %s: status %d", ap.name, path, code)
					}
				})
			}
		}
	}
	close(start)
	wg.Wait()
	mapAfterRequests := a.requests.size()
	close(gate.release)
	gate.wg.Wait()
	rels := exp.take()
	want := len(apps) * perPath * 3
	if len(rels) != want {
		t.Fatalf("releases=%d want %d", len(rels), want)
	}
	panics, late := 0, 0
	for _, r := range rels {
		if count(r.Spans, isServer) != 1 || !isServer(root(r)) {
			t.Fatalf("release without single root: %s", describe(r.Spans))
		}
		if st, _ := attr(root(r).Attributes(), "http.response.status_code"); st.AsInt64() == 500 {
			panics++
		}
		if named(r.Spans, "late") != nil {
			late++
		}
	}
	if panics != len(apps)*perPath || late != 0 {
		t.Fatalf("panics=%d late=%d", panics, late)
	}
	requireEmptyMap(t, a)
	t.Logf("mode=%s apps=%d requests=%d releases=%d panicReleases(status 500)=%d lateSpansExported=%d mapAfterRequests=%d mapAfterLateSpans=%d userSpans=%d",
		a.mode, len(apps), len(apps)*len(paths)*perPath, len(rels), panics, late, mapAfterRequests, a.requests.size(), len(user.GetSpans()))
}

type recordOnlySampler struct{}

func (recordOnlySampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	return sdktrace.SamplingResult{Decision: sdktrace.RecordOnly}
}

func (recordOnlySampler) Description() string { return "RecordOnly" }

func nestedRequest(t *testing.T) {
	user := attachUser(sdktrace.AlwaysSample())
	a, exp := newApitally(Config{SampleRate: 1})
	var inner http.Handler
	mux := http.NewServeMux()
	mux.HandleFunc("GET /outer", func(w http.ResponseWriter, r *http.Request) {
		handlerSpan(r.Context(), "outer-work")
		inner.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(r.Context(), "GET", "/inner", nil))
	})
	mux.HandleFunc("GET /inner", func(w http.ResponseWriter, r *http.Request) { handlerSpan(r.Context(), "inner-work") })
	inner = a.Middleware(mux)
	g := gin.New()
	g.Use(a.Gin())
	g.GET("/outer", func(c *gin.Context) {
		handlerSpan(c.Request.Context(), "outer-work")
		c.Request.URL.Path = "/inner"
		g.HandleContext(c)
	})
	g.GET("/inner", func(c *gin.Context) { handlerSpan(c.Request.Context(), "inner-work") })
	do := map[string]func(){
		"nethttp sub-request": func() { inner.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/outer", nil)) },
		"gin HandleContext":   func() { g.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/outer", nil)) },
	}
	for _, name := range []string{"nethttp sub-request", "gin HandleContext"} {
		do[name]()
		rels := exp.take()
		us := takeUser(user).Snapshots()
		if len(rels) != 2 {
			t.Fatalf("%s: want 2 releases, got %d (map entries %d)", name, len(rels), a.requests.size())
		}
		for _, r := range rels {
			if count(r.Spans, isServer) != 1 || !isServer(root(r)) || len(r.Spans) != 2 {
				t.Fatalf("%s: release %s", name, describe(r.Spans))
			}
		}
		if root(rels[0]).Parent().SpanID() != root(rels[1]).SpanContext().SpanID() {
			t.Fatalf("%s: inner root is not a child of the outer root", name)
		}
		t.Logf("%s: inner release=%s outer release=%s user=%s", name, describe(rels[0].Spans), describe(rels[1].Spans), describe(us))
		requireEmptyMap(t, a)
	}
}
