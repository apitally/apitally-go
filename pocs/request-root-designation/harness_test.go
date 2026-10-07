package rootpoc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	fiberotel "github.com/gofiber/contrib/v3/otel"
	"github.com/gofiber/fiber/v3"
	fiberrecover "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

const scenarioEnv = "ROOTPOC_SCENARIO"

const (
	scopeOtelhttp  = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	scopeOtelgin   = "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	scopeFiberOtel = "github.com/gofiber/contrib/v3/otel"
	headerAttr     = "http.response.header.content-type"
)

type scenario struct {
	name string
	run  func(*testing.T)
}

func TestScenarios(t *testing.T) {
	if name := os.Getenv(scenarioEnv); name != "" {
		gin.SetMode(gin.ReleaseMode)
		i := slices.IndexFunc(scenarios, func(s scenario) bool { return s.name == name })
		if i < 0 {
			t.Fatalf("unknown scenario %q", name)
		}
		scenarios[i].run(t)
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			cmd := exec.Command(binary, "-test.run=^TestScenarios$", "-test.v", "-test.count=1", "-test.timeout=120s")
			cmd.Env = append(os.Environ(), scenarioEnv+"="+s.name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			for line := range strings.Lines(string(out)) {
				if strings.HasPrefix(line, "    ") {
					t.Log(strings.TrimSpace(line))
				}
			}
		})
	}
}

type apitallyExporter struct {
	mu       sync.Mutex
	releases []Release
}

func (e *apitallyExporter) export(r Release) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releases = append(e.releases, r)
}

func (e *apitallyExporter) take() []Release {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.releases
	e.releases = nil
	return out
}

func newApitally(cfg Config) (*Apitally, *apitallyExporter) {
	exp := &apitallyExporter{}
	return New(cfg, exp.export), exp
}

func attachUser(sampler sdktrace.Sampler) *tracetest.InMemoryExporter {
	exp := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler), sdktrace.WithSyncer(exp)))
	return exp
}

type foreignProvider struct {
	embedded.TracerProvider
	sdk *sdktrace.TracerProvider
}

func (f *foreignProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return f.sdk.Tracer(name, opts...)
}

func takeUser(exp *tracetest.InMemoryExporter) tracetest.SpanStubs {
	out := exp.GetSpans()
	exp.Reset()
	return out
}

func handlerSpan(ctx context.Context, name string) {
	_, s := otel.Tracer("handler").Start(ctx, name)
	s.End()
}

type lateGate struct {
	release chan struct{}
	wg      sync.WaitGroup
}

func (g *lateGate) spawn(ctx context.Context) {
	if g == nil {
		return
	}
	g.wg.Go(func() {
		<-g.release
		handlerSpan(ctx, "late")
	})
}

type appOptions struct {
	outer, inner bool
	late         *lateGate
}

type app struct {
	name string
	do   func(path string) int
}

const body = `{"ok":true}`

func netApp(a *Apitally, o appOptions) app {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /work/{id}", func(w http.ResponseWriter, r *http.Request) {
		handlerSpan(r.Context(), "work")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { handlerSpan(r.Context(), "healthz-work") })
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("GET /late", func(w http.ResponseWriter, r *http.Request) { o.late.spawn(r.Context()) })
	var h http.Handler = mux
	if o.inner {
		h = otelhttp.NewHandler(h, "inner")
	}
	h = a.Middleware(h)
	if o.outer {
		h = otelhttp.NewHandler(h, "outer")
	}
	h = recoverer(h)
	return app{name: label("nethttp", o), do: func(path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code
	}}
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				w.WriteHeader(500)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func ginApp(a *Apitally, o appOptions) app {
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard))
	if o.outer {
		r.Use(otelgin.Middleware("svc"))
	}
	r.Use(a.Gin())
	if o.inner {
		r.Use(otelgin.Middleware("svc"))
	}
	r.GET("/work/:id", func(c *gin.Context) {
		handlerSpan(c.Request.Context(), "work")
		c.Data(200, "application/json", []byte(body))
	})
	r.GET("/healthz", func(c *gin.Context) { handlerSpan(c.Request.Context(), "healthz-work") })
	r.GET("/panic", func(*gin.Context) { panic("boom") })
	r.GET("/late", func(c *gin.Context) { o.late.spawn(c.Request.Context()) })
	return app{name: label("gin", o), do: func(path string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code
	}}
}

func fiberApp(t *testing.T, a *Apitally, o appOptions) app {
	f := fiber.New()
	f.Use(fiberrecover.New())
	if o.outer {
		f.Use(fiberotel.Middleware(fiberotel.WithoutMetrics(true)))
	}
	f.Use(a.Fiber())
	if o.inner {
		f.Use(fiberotel.Middleware(fiberotel.WithoutMetrics(true)))
	}
	f.Get("/work/:id", func(c fiber.Ctx) error {
		handlerSpan(c.Context(), "work")
		c.Set(fiber.HeaderContentType, "application/json")
		return c.SendString(body)
	})
	f.Get("/healthz", func(c fiber.Ctx) error { handlerSpan(c.Context(), "healthz-work"); return nil })
	f.Get("/panic", func(fiber.Ctx) error { panic("boom") })
	f.Get("/late", func(c fiber.Ctx) error { o.late.spawn(c.Context()); return nil })
	f.Get("/stream", func(c fiber.Ctx) error {
		ctx := c.Context()
		return c.SendStreamWriter(func(w *bufio.Writer) {
			handlerSpan(ctx, "stream-write")
			_, _ = w.WriteString("chunk")
		})
	})
	return app{name: label("fiber", o), do: func(path string) int {
		resp, err := f.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Errorf("fiber %s: %v", path, err)
			return 0
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}}
}

func label(framework string, o appOptions) string {
	switch {
	case o.outer:
		return framework + "+outer"
	case o.inner:
		return framework + "+inner"
	}
	return framework
}

func allApps(t *testing.T, a *Apitally, o appOptions) []app {
	return []app{netApp(a, o), ginApp(a, o), fiberApp(t, a, o)}
}

func attr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func keys(attrs []attribute.KeyValue) string {
	out := make([]string, 0, len(attrs))
	for _, kv := range attrs {
		out = append(out, string(kv.Key))
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

func root(r Release) sdktrace.ReadOnlySpan { return r.Spans[len(r.Spans)-1] }

func describe(spans []sdktrace.ReadOnlySpan) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, fmt.Sprintf("%s[%s,%s,%s]", s.Name(), s.SpanKind(), shortScope(s.InstrumentationScope().Name), s.SpanContext().SpanID()))
	}
	return strings.Join(parts, " ")
}

func describeStubs(stubs tracetest.SpanStubs) string { return describe(stubs.Snapshots()) }

func shortScope(name string) string {
	switch name {
	case scopeName:
		return "apitally"
	case scopeOtelhttp:
		return "otelhttp"
	case scopeOtelgin:
		return "otelgin"
	case scopeFiberOtel:
		return "fiberotel"
	}
	return name
}

func count[T any](items []T, pred func(T) bool) int {
	n := 0
	for _, it := range items {
		if pred(it) {
			n++
		}
	}
	return n
}

func isServer(s sdktrace.ReadOnlySpan) bool { return s.SpanKind() == trace.SpanKindServer }

func serverOf(spans []sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if isServer(s) {
			return s
		}
	}
	return nil
}

func named(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func requireEmptyMap(t *testing.T, a *Apitally) {
	t.Helper()
	if n := a.requests.size(); n != 0 {
		t.Fatalf("request map has %d leaked entries", n)
	}
}

func requireOneRelease(t *testing.T, what string, rels []Release) Release {
	t.Helper()
	if len(rels) != 1 {
		t.Fatalf("%s: want 1 Apitally release, got %d", what, len(rels))
	}
	r := rels[0]
	if n := count(r.Spans, isServer); n != 1 || !isServer(root(r)) {
		t.Fatalf("%s: want exactly one SERVER span as root, got %d: %s", what, n, describe(r.Spans))
	}
	return r
}
