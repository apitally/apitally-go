package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	root "github.com/apitally/apitally-go"
)

var (
	registrationMu sync.Mutex
	currentRuntime atomic.Pointer[sdkRuntime]
	// Activation is suppressed in test binaries unless a test calls SetUpTest.
	isActivationAllowedInTests atomic.Bool
)

// sdkRuntime is the process-global Apitally runtime. The first registered
// configuration wins; components are created once at activation.
type sdkRuntime struct {
	settings  *settings
	framework FrameworkInfo

	routeListersMu sync.Mutex
	routeListers   []func() []Route

	activateOnce sync.Once
	active       atomic.Bool

	resource *resourcepb.Resource
	spool    *spool
	client   *exportClient
	logs     *logBatcher
	metrics  *metrics

	cycleMu        sync.Mutex
	exportInterval time.Duration
	stopExportLoop context.CancelFunc
	exportLoopDone chan struct{}
}

// Register resolves the configuration into the process-global runtime on the
// first call, compares later configurations with it and records the app's
// route listing for the startup event.
func Register(cfg *root.Config, framework FrameworkInfo, listRoutes func() []Route) {
	defer recoverAndLogPanic("registration")
	s := resolveSettings(cfg)
	registrationMu.Lock()
	defer registrationMu.Unlock()
	r := currentRuntime.Load()
	if r == nil {
		for _, msg := range s.configErrors {
			logError(msg)
		}
		r = &sdkRuntime{settings: s, framework: framework}
		currentRuntime.Store(r)
	} else if !r.settings.isEquivalent(s) {
		logWarn("Apitally was initialized again with a different configuration, which is ignored")
	}
	r.routeListersMu.Lock()
	r.routeListers = append(r.routeListers, listRoutes)
	r.routeListersMu.Unlock()
}

// Activate starts Apitally once per process. Concurrent callers wait until
// activation has completed.
func Activate() {
	if r := currentRuntime.Load(); r != nil {
		r.activateOnce.Do(r.activate)
	}
}

// Shutdown runs the final export cycle within the context's deadline and
// returns the context's error if it expires first.
func Shutdown(ctx context.Context) error {
	r := currentRuntime.Load()
	if r == nil {
		return nil
	}
	r.activateOnce.Do(func() {})
	if !r.active.CompareAndSwap(true, false) {
		return nil
	}
	return r.shutdown(ctx)
}

// SetUpTest allows activation in the test binary. When the test ends, it shuts
// the runtime down, clears it and restores the OTel globals.
func SetUpTest(t testing.TB) {
	isActivationAllowedInTests.Store(true)
	t.Cleanup(func() {
		if r := currentRuntime.Swap(nil); r != nil && r.active.CompareAndSwap(true, false) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.shutdown(ctx)
			cancel()
		}
		warnedKeys.Clear()
		isActivationAllowedInTests.Store(false)
	})
}

func (r *sdkRuntime) activate() {
	if !r.settings.enabled || (testing.Testing() && !isActivationAllowedInTests.Load()) {
		return
	}
	defer recoverAndLogPanic("activation")
	r.resource = encodeResource(newResource(r.settings.config.Env))
	r.spool = newSpool()
	r.client = newExportClient(r.settings)
	r.logs = newLogBatcher(r.spool, r.resource)
	r.metrics = newMetrics(r.spool, r.resource)
	r.logs.emitEvent(startupEventName, startupEventBody(r.settings, r.framework, r.listRoutes()))
	ctx, cancel := context.WithCancel(context.Background())
	r.exportInterval, r.stopExportLoop, r.exportLoopDone = defaultExportInterval, cancel, make(chan struct{})
	go r.logs.run()
	go r.runExportLoop(ctx)
	r.active.Store(true)
}

func (r *sdkRuntime) shutdown(ctx context.Context) error {
	defer recoverAndLogPanic("shutdown")
	r.stopExportLoop()
	select {
	case <-r.exportLoopDone:
	case <-ctx.Done():
	}
	r.cycleMu.Lock()
	defer r.cycleMu.Unlock()
	r.logs.shutdown(ctx)
	r.metrics.collect()
	r.spool.closeCurrentFiles()
	r.sendPendingFiles(ctx, -1)
	r.spool.deleteAll()
	r.client.close()
	return ctx.Err()
}

// listRoutes returns the union of the routes of all registered apps.
func (r *sdkRuntime) listRoutes() []Route {
	r.routeListersMu.Lock()
	defer r.routeListersMu.Unlock()
	var routes []Route
	for _, listRoutes := range r.routeListers {
		func() {
			defer recoverAndLogPanic("route listing")
			routes = append(routes, listRoutes()...)
		}()
	}
	return routes
}
