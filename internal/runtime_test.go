package internal

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

var testFramework = FrameworkInfo{Name: "nethttp", ModulePath: "std", ScopeName: "github.com/apitally/apitally-go/internal"}

// registerForTest registers cfg, exporting to server through its in-process
// transport, so tests can run in synctest bubbles. The slog default handler
// captures logs and discards the application's output.
func registerForTest(t *testing.T, server *testutils.OTLPServer, cfg *root.Config, routes ...Route) {
	t.Helper()
	SetUpTest(t)
	testutils.SetSlogDefault(t, NewSlogHandler(slog.NewTextHandler(io.Discard, nil)))
	setExportTransportForTest(t, server.Transport())
	Register(cfg, testFramework, func() []Route { return routes })
}

func startRuntimeForTest(t *testing.T, server *testutils.OTLPServer, cfg *root.Config, routes ...Route) {
	t.Helper()
	registerForTest(t, server, cfg, routes...)
	Activate()
}

func TestActivationEmitsStartupEventOnce(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.AppVersion = "1.2.3"
	cfg.ExcludePaths = []string{"/internal/"}
	cfg.SampleOnResponse = func(span sdktrace.ReadOnlySpan) (float64, bool) { return 1, true }
	registerForTest(t, server, cfg, Route{"GET", "/items"}, Route{"HEAD", "/items"}, Route{"POST", "/items/{id}"}, Route{"GET", "/items"})
	Register(cfg, testFramework, func() []Route { return []Route{{"POST", "/orders"}} })

	Activate()
	Activate()
	require.NoError(t, Shutdown(context.Background()))

	records := server.LogRecords(t)
	require.Len(t, records, 1)
	assert.Equal(t, "apitally", records[0].Scope)
	assert.Empty(t, records[0].TraceId)
	var body map[string]any
	server.DecodeStartupEvent(t, &body)
	assert.Equal(t, map[string]any{
		"framework": "nethttp",
		"versions":  map[string]any{"go": strings.TrimPrefix(runtime.Version(), "go"), "nethttp": "unknown", "app": "1.2.3"},
		"config": map[string]any{
			"CaptureLogs":            true,
			"CaptureRequestHeaders":  false,
			"CaptureRequestBody":     false,
			"CaptureResponseHeaders": true,
			"CaptureResponseBody":    false,
			"SampleRate":             1.0,
			"SampleOnResponse":       true,
			"MaskQueryParams":        []any{},
			"MaskHeaders":            []any{},
			"MaskBodyFields":         []any{},
			"ExcludePaths":           []any{"(?i)/internal/"},
		},
		"paths": []any{
			map[string]any{"method": "GET", "path": "/items"},
			map[string]any{"method": "POST", "path": "/items/{id}"},
			map[string]any{"method": "POST", "path": "/orders"},
		},
	}, body)
	resource := testutils.Attributes(records[0].Resource.Attributes)
	assert.Equal(t, "dev", resource["deployment.environment.name"])
	assert.Equal(t, "apitally-go", resource["telemetry.distro.name"])
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, resource["service.instance.id"])
}

func TestActivationIsSuppressedInTestBinaries(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	SetUpTest(t)
	isActivationAllowedInTests.Store(false)
	Register(nil, testFramework, nil)

	Activate()
	require.NoError(t, Shutdown(context.Background()))

	assert.Empty(t, server.Requests())
}

func TestActivationWarnsWhenNoSlogHandlerWasCreated(t *testing.T) {
	for _, captureLogs := range []bool{true, false} {
		t.Run(strconv.FormatBool(captureLogs), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			SetUpTest(t)
			setExportTransportForTest(t, server.Transport())
			logs := testutils.RecordSlog(t)
			cfg := root.NewConfig()
			cfg.CaptureLogs = captureLogs
			Register(cfg, testFramework, nil)

			Activate()
			require.NoError(t, Shutdown(context.Background()))

			assert.Equal(t, captureLogs, slices.ContainsFunc(logs.Messages(slog.LevelWarn), func(msg string) bool { return strings.Contains(msg, "NewSlogHandler") }))
		})
	}
}

func TestIdleShutdownDeliversProcessGauges(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	startRuntimeForTest(t, server, nil)

	require.NoError(t, Shutdown(context.Background()))

	var names []string
	for _, metric := range server.Metrics(t) {
		assert.Equal(t, "apitally", metric.Scope)
		names = append(names, metric.Name)
	}
	assert.Equal(t, []string{"process.cpu.utilization", "process.memory.usage", "process.uptime"}, names)
}

func TestShutdownHonorsContextDeadline(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	synctest.Test(t, func(t *testing.T) {
		startRuntimeForTest(t, server, nil)
		release := server.Hold()
		defer release()
		// A Fiber shutdown hook can still be flushing when the application calls Shutdown.
		go Flush()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		start := time.Now()
		err := Shutdown(ctx)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, time.Second, time.Since(start))
	})
}
