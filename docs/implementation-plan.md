---
directory: /Users/simon.gurcke/Repos/apitally/apitally-go
implemented_at: 2026-10-07T21:33:31+10:00
---

# Apitally Go v1 implementation plan

Status: Proposed, 2026-10-07.

## 1. Scope and authority

Implement [the Go design](design.md) on the `v1` branch: an OpenTelemetry distribution shipped as v1 of the root module and six framework modules. The [shared specification](../../cloud/docs/sdks/spec.md) owns wire contracts, the [shared design](../../cloud/docs/sdks/design.md) owns cross-SDK behavior, and the Go design records Go adaptations and their evidence.

This plan is the implementation authority. Where it departs from design.md (section 2), the plan wins; design.md stays the record of rationale and facts.

The architecture in one paragraph: each framework module's `Init` registers framework middleware that hands request data to one flat `internal` package. That package owns the process-global runtime, provider selection, request registry, sampling, capture, redaction, logs, metrics, OTLP encoding, the spool and the export worker. The root package declares only the public types and `NewConfig`. Three background goroutines run: the stock span batch processor, the log batcher and the export worker.

## 2. Decisions made during planning

Decisions 1 and 9 changed design.md, which has been updated to match. The others are not in design.md.

1. **The root package declares only the public types.** `github.com/apitally/apitally-go` (package `apitally`) contains `Config`, `NewConfig`, `Consumer` and `LogRecord`, and imports nothing from the SDK. The `internal` package imports root for these types. `Shutdown`, `NewSlogHandler` and the request helpers exist only in the framework packages, as documented wrapper functions that call `internal` directly. Design sections 4, 9 and 13 have been updated to match. Users never import root, so functions there would have no caller, and they would create an import cycle between root and `internal`. `CaptureError`, which records its caller's stack, therefore has exactly one public entry depth.
2. **One flat `internal` package**, as in v0 and as the standard library organizes `net/http`. The SDK's components form one connected system (the request registry is used by the middleware, span processor, slog handler and helpers; release feeds spans, logs and metrics), and splitting it would add cycle-breaking interfaces without clarity.
3. **Shared test helpers live in `internal/testutils`**, a separate package so testify and the stub OTLP endpoint never compile into users' binaries. It never imports `internal`, because `internal`'s own tests import it.
4. **Build order:** the core is built and tested on `net/http` and Chi first, then Gin and Echo, then Fiber, then release work (section 8).
5. **CI matrix:** every module (root plus six frameworks) runs at its declared floors on Go 1.25, and with all dependencies upgraded to their latest releases (`go get -u -t ./...`) on Go 1.26 and on Go 1.27, with `GOTOOLCHAIN=local`. This covers newer OTel releases, which users on Go 1.26+ resolve.
6. **SDK test harness:** six Go apps in `../sdk-tests`, one per framework module, sharing a Go `core` module, each run at its framework's floor and latest release, plus a Fiber prefork variant.
7. **Execution:** each stage in section 8 is one commit on `v1`, made when `GOTOOLCHAIN=go1.25.14 make check test` passes. Go 1.27.1 is installed through mise and downloads the 1.25.14 toolchain, so with the floor versions in `go.mod` each stage passes CI's floor job; a `go 1.25` directive does not stop code from calling standard library APIs added in 1.26 or 1.27. Stages run without review stops. Nothing is pushed.
8. **The SDK version comes from Go's build info.** The SDK reports the version that `debug.ReadBuildInfo()` records for `github.com/apitally/apitally-go`, as the main module or as a dependency, or `unknown` when the build embeds no module information (Bazel). Tests never pin the value. There is no version constant: the root module's tag is the GitHub release tag, created before the publish workflow commits, so a constant stamped by the workflow never reaches users (v0 reports `0.0.0` for this reason).
9. **No `StartSpan`.** Documentation shows `otel.Tracer(...).Start` with the request context instead (design section 13, updated). A wrapper would still return OTel's `trace.Span`, so it would not spare users the OTel API, and on Gin and Fiber it would fail silently when passed the framework context, exactly like `otel.Tracer`.

## 3. Repository layout

The end state. Files appear in the stage that first needs them; no stage creates placeholders.

```text
go.mod                         root module, go 1.25
types.go                       public Config, NewConfig, Consumer, LogRecord (package doc comment)
internal/
  config.go                    resolution of Config: env vars, token check, sample rate, patterns, equality
  diagnostics.go               SDK logging through slog.Default with logger=apitally, warn-once deduplication
  runtime.go                   process-global runtime, Init registration, activation, test guard, Shutdown
  resource.go                  resource construction, service.instance.id, Apitally-owned keys, SDK version from build info
  providers.go                 captured globals, provider selection, span limits, propagator
  server_span.go               SERVER span creation or reuse, propagation extract, stable semconv attributes
  requests.go                  BeginRequest, exclusions, request state, in-flight registry, root designation, buffers, FinishObservation, release
  sampling.go                  trace-ID ratio test, request and response sampling with callbacks, fallback sampler
  span_processor.go            OnStart linking and OnEnd buffering for request members
  span_exporter.go             export copy embedding sdktrace.ReadOnlySpan with payload stash, stash processing, applying query redaction to every span, delegation to otlptrace
  body_capture.go              body capture decisions from headers, content-type allowlist, bounded capture buffer
  nethttp.go                   net/http middleware used by Chi, Request.Body wrapper (counting, capture, EOF tracking), http.ResponseWriter wrapper (interface forwarding, counting, capture) also used by Echo and Gin
  fasthttp.go                  Fiber completion closer, stream sizes, stream wrapper, unsafe field write
  body_processing.go           bounded decompression, body mask callbacks, JSON parse and compaction
  redaction.go                 query, header and body-field redaction functions, default patterns
  server_errors.go             error capture, panic capture used by every middleware's deferred recover, exception fields, deterministic stack formatting, bounded server error groups
  validation_errors.go         validator recognition by method set, validation details, bounded validation error groups
  consumers.go                 consumer normalization, merge, LRU of payload hashes, consumer-update events
  helpers.go                   request-state lookup, SetConsumer, SetRequestAttributes, CaptureError, CaptureValidationError
  slog_handler.go              capture handler, value conversion, MaskLogRecord invocation, truncation, request-logger exclusion
  log_batcher.go               internal log record type shared by captured logs and SDK events, log batching goroutine, log spool appends
  startup.go                   startup event with path filtering and union across apps
  histogram.go                 base-2 exponential histogram at scale 3
  metrics.go                   request histogram aggregation, capacity, collection, CPU, memory and uptime gauges via gopsutil
  otlp_encoding.go             attribute, resource, log and metric encoding to OTLP protobuf
  spool.go                     gzip spool files, rotation, caps, retention, memory fallback, orphan cleanup
  export_worker.go             export cycles, send budget, final drain
  export_client.go             private http.Client, headers, response classification
  testutils/                   stub OTLP endpoint (httptest.Server and in-process http.RoundTripper), payload decoding, slog.Default restore
chi-v5/  echo-v4/  echo-v5/  fiber-v2/  fiber-v3/  gin-v1/
  go.mod                       framework floor, root via replace ../
  middleware.go                framework-specific code: Init, middleware, error dispatch, route listing
  sdk.go                       framework-independent public API, identical in every module: type aliases and wrapper functions
  response_writer.go           gin-v1 only: gin.ResponseWriter wrapper
docs/                          design.md, reviews, this plan
README.md  MIGRATION.md  Makefile  .github/workflows/
```

Each source file has a `_test.go` sibling when it has observable behavior worth pinning (section 9). In framework modules, `middleware_test.go` holds the integration scenarios and `sdk_test.go` the `CaptureError` caller-frame test.

## 4. Public API

### Root package

- `Config` with the fields listed in design section 3, each with a doc comment stating its default and env var fallback. Callback field types: `SampleOnRequest`, `SampleOnResponse func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)`; `MaskRequestBody`, `MaskResponseBody func(span sdktrace.ReadOnlySpan, body []byte) []byte`; `MaskLogRecord func(record *LogRecord) bool`. Pattern fields are `[]string`.
- `NewConfig() *Config` sets `CaptureLogs`, `CaptureResponseHeaders` and `SampleRate`.
- `Consumer struct { Identifier, Name, Group string; Attributes map[string]string }`.
- `LogRecord struct { Time time.Time; Level slog.Level; Message string; Attrs []slog.Attr }`. Code location and request linkage live in the internal record that holds the `LogRecord` by value, so the callback receives `&captured.Record` with no copy.

### Framework packages

Each framework package (package name `apitally`) imports root as `root "github.com/apitally/apitally-go"` and declares:

- `Init(app, cfg *Config)` with the framework's typed app argument (design section 13).
- `type Config = root.Config`, `type Consumer = root.Consumer`, `type LogRecord = root.LogRecord`, and `func NewConfig() *Config`.
- `Shutdown(ctx) error`, `NewSlogHandler(next slog.Handler) slog.Handler`, `SetConsumer(ctx, Consumer)`, `SetRequestAttributes(ctx, attrs...)`, `CaptureError(ctx, err)`, `CaptureValidationError(ctx, err)`.

Every wrapper carries its own user-facing doc comment and delegates in one call. `CaptureError` passes a fixed frame-skip count to `internal`, so the recorded stack starts at the user's frame.

### Inside `internal`

Exported identifiers exist only for framework modules and their tests. The central types and calls, used by every framework middleware:

- `Register(cfg *root.Config, framework FrameworkInfo, listRoutes func() []Route)`: resolves configuration into the process-global runtime on first call, compares later configurations (design section 3) and records the app's route-listing function for the startup event. `Route` holds method and path. `FrameworkInfo` carries the framework name, module path (instrumentation scope) and framework module path for the version lookup.
- `Activate()`: the `sync.Once` activation, called by middleware on every request and by Fiber's `OnListen`.
- `BeginRequest(ctx, RequestInfo) (*RequestState, context.Context)`: span selection (design section 8), exclusions, request sampling, root registration and request-state storage in the context. `RequestInfo` holds method, scheme, host, path, query, headers, client address and the content-length header, all copied from the framework.
- `(*RequestState).FinishObservation(TransportResult)`: final route, status, sizes, captured bodies and headers, error state; records metrics, error aggregates and the consumer update; ends a created SERVER span; marks transport complete for release coordination.

Framework code never touches the registry, spans or sampling directly.

## 5. Component notes

Behavior is specified in design.md; this section records implementation choices it leaves open.

**Configuration and runtime.** `internal/config.go` resolves a `root.Config` copy into an unexported `settings` struct with compiled patterns. The runtime is one package-level value guarded by a mutex; activation uses `sync.Once`. `testing.Testing()` blocks activation unless the exported test hook `SetUpTest(t testing.TB)` was called. It allows activation and registers a `t.Cleanup` that shuts the runtime down, clears it and restores the captured OTel globals. Root tests call it directly and framework tests call `internal.SetUpTest(t)`; `testutils` cannot, because it never imports `internal`. Restoring works because `otel.SetTracerProvider` accepts the original default provider whenever the current global is a different provider.

**Request registry.** A `map[trace.SpanID]*RequestState` under one mutex holds request roots and linked members. `RequestState` has its own mutex for buffers, completion flags, consumer and error state. Release happens when both transport completion and SERVER span end are recorded, in the order descendants, SERVER span, logs (design section 6). Release removes only entries the request still owns. Released spans go to the batch span processor through `OnEnd`; logs go to the log batcher. Per-request caps are 1,000 spans and 1,000 log records.

**Batch settings.** The stock `sdktrace.NewBatchSpanProcessor` with queue 2,048, batch 512, delay 1s and an explicit export timeout. The log batcher uses the same numbers. Both encode in sub-chunks of at most 32 records before appending to the spool (shared design section 10).

**Span export.** Apitally's span exporter processes each export copy's stash on the batch goroutine (decompression, body masks, JSON field redaction), redacts query-bearing attributes on every span, then delegates to `otlptrace.NewUnstarted` with an Apitally `otlptrace.Client` whose `UploadTraces` wraps the `ResourceSpans` in `TracesData` and appends to the trace spool. A privacy failure drops that span.

**Fiber without importing fasthttp.** `internal/fasthttp.go` works on method-set interfaces (`SetUserValue(key, value any)`, `BodyStream() io.Reader`, `IsBodyStream() bool`), all present from fasthttp v1.50.0. The stream wrapper is installed by one function that takes the `*fasthttp.Response` as `any`, finds the unexported `bodyStream` field with `reflect`, checks its name and `io.Reader` type once per process and writes it through `reflect.NewAt`. On mismatch nothing is wrapped (design section 7).

**OTLP encoding.** `go.opentelemetry.io/proto/otlp` v1.11.0 data packages only. Logs and metrics are encoded by `otlp_encoding.go`; traces by `otlptrace`. Resource: OTel's standard resource detection plus `service.instance.id` (random UUIDv4 from `crypto/rand`, formatted locally, no uuid dependency), `deployment.environment.name`, `telemetry.distro.name = apitally-go` and `telemetry.distro.version` set to the SDK version (decision 8). Export headers include `User-Agent: apitally-go/<SDK version>`.

**Export worker.** One goroutine, timers created with `time.NewTimer`, so tests can run it inside a `testing/synctest` bubble. Fake time only advances when every goroutine in the bubble is blocked on something synctest controls, and the HTTP client's idle keep-alive connections wait on network reads, which do not count. Timer-driven tests therefore export through the `testutils` in-process `http.RoundTripper`, which calls the stub endpoint's handler directly, installed with the unexported test hook `setExportTransportForTest(t, transport)`; only root tests drive timers. It runs the cycle from shared design section 10: drain error aggregates, flush the log batcher, `ForceFlush` the span processor, collect metrics, rotate and send. Final drain on `Shutdown` honors the context deadline. Fiber shutdown hooks run one non-terminal cycle with a fixed 5-second deadline.

**Panics.** Every SDK goroutine and every user callback invocation runs under a recover that reports through `diagnostics.go` and applies the documented fallback. Application panics observed by middleware are re-panicked unchanged.

## 6. Framework integrations

Common to all: `Init` calls `internal.Register`, installs middleware once per app (a per-app guard stored in an `internal` map keyed by the app pointer), and on Gin and Fiber logs the late-`Init` error when routes already exist. Middleware calls `internal.Activate`, `BeginRequest`, runs the chain, then `FinishObservation`; on Fiber, for a wrapped stream of unknown length, the completion closer calls `FinishObservation` when fasthttp finishes or aborts the response. Route listing functions follow design section 9.

- **Chi:** `r.Use(internal.NetHTTPMiddleware(...))`. The route comes from `chi.RouteContext(r.Context()).RoutePattern()` after the handler returns. Route listing via `chi.Walk`.
- **Gin:** `engine.Use`, after the application's recovery middleware, as with the other frameworks. A panic is recorded with the committed status, or an assumed 500 before the response started. `gin-v1/response_writer.go` implements `gin.ResponseWriter` over the shared counting and capture logic. Request state is also stored with `c.Set` under a namespaced key. Errors come from `c.Errors`.
- **Echo v4 and v5:** `e.Use`, response writer replaced with the shared `net/http` wrapper, request context updated with `c.SetRequest`. Errors returned by the chain are dispatched with `c.Error(err)` (v4) or `c.Echo().HTTPErrorHandler(c, err)` (v5), and the middleware returns nil.
- **Fiber v2 and v3:** `app.Use`, completion closer stored as a user value, request data copied before return, request state stored in `Locals`. Errors dispatched with `c.App().ErrorHandler(c, err)` with the `SendStatus(500)` fallback, returning nil. `OnListen` activates unless prefork is set. Shutdown hooks flush. v2 handlers pass `c.Context()` or `c.UserContext()`; v3 handlers pass `c`.

## 7. Dependencies

Add each dependency in the stage that first imports it; `go mod tidy` keeps the files exact.

| Module | Version floor | Used by |
| --- | --- | --- |
| `go.opentelemetry.io/otel`, `/sdk`, `/trace` | v1.46.0 | root |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace` | v1.46.0 | root |
| `go.opentelemetry.io/proto/otlp` | v1.11.0 | root |
| `google.golang.org/protobuf` | as required by proto/otlp v1.11.0 | root |
| `github.com/shirou/gopsutil/v4` | v4.26.8 | root |
| `github.com/stretchr/testify` | current | tests only |
| Chi v5.1.0, Echo v4.11.4, Echo v5.0.4, Fiber v2.51.0, Fiber v3.0.0, Gin v1.9.1 | as listed | framework modules |

Framework modules require `github.com/apitally/apitally-go v0.0.0` with `replace github.com/apitally/apitally-go => ../`, as v0 does; the publish workflow rewrites the version at release. All `go` directives are `1.25`. Remove v0-only dependencies (`google/uuid`, `go-retryablehttp`, `golang.org/x/sys` unless gopsutil needs it).

## 8. Implementation sequence

Each stage builds on earlier stages only, adds its tests, passes `GOTOOLCHAIN=go1.25.14 make check test`, and becomes one commit on `v1`. Later stages add call sites and data to earlier code; they never fill in placeholders. Port knowledge from v0 on `main` (route enumeration, JSON field masking, response writer forwarding, gopsutil usage, test scenarios) after reviewing each piece against the v1 contract, and port the request-root logic from `pocs/request-root-designation`.

| Stage | Work |
| --- | --- |
| 1. Foundation | Delete `common/`, the v0 `internal/` files, all six framework module directories and stray `coverage.out` files. Rewrite root `go.mod` for Go 1.25. Add the root `types.go`, `internal/config.go` and `internal/diagnostics.go`. Rewrite the Makefile so `check` and `test` cover the root module plus a `MODULES` list that later stages extend. Rewrite `tests.yaml` for the section 10 matrix over the modules present, and add `v1` to its push trigger. |
| 2. Runtime and export pipeline | `runtime.go` (registration, configuration comparison, activation, test guard, `Shutdown`), `resource.go` with the SDK version, `otlp_encoding.go`, `spool.go`, `export_client.go`, `export_worker.go`, `log_batcher.go` with the internal log record type, the process gauges in `metrics.go`, `startup.go`, the test hooks, and `internal/testutils` with the stub OTLP endpoint, in-process transport and payload decoding. Tests activate the runtime directly and receive the startup event and process gauges at the stub endpoint. |
| 3. Request tracing on net/http and Chi | `providers.go`, `server_span.go`, `requests.go`, `sampling.go`, `span_processor.go`, `span_exporter.go` with the export copy and query redaction, `nethttp.go` with a status-recording writer, and `SetRequestAttributes` in `helpers.go`. Create `chi-v5` with `Init`, route listing, the wrappers implemented so far and the canonical scenarios they support. Add `chi-v5` to the Makefile and CI. |
| 4. Capture and request metrics | `body_capture.go`, the full response writer wrapper (`Flusher`, `Hijacker`, `Pusher`, conditional `ReaderFrom`, `Unwrap`) and the request body wrapper in `nethttp.go`, header capture, `body_processing.go`, `redaction.go`, size resolution, `histogram.go`, and request histograms in `metrics.go` with the capacity warning. |
| 5. Errors, validation and consumers | Panic capture in `server_errors.go`, called from the deferred recover in `nethttp.go` before re-panicking, `server_errors.go` and `validation_errors.go` with their aggregates and events, `consumers.go` with consumer-update events, and the remaining helpers. Chi gains `SetConsumer`, `CaptureError` and `CaptureValidationError`. |
| 6. Application logs | `slog_handler.go`, request linkage through request state, the request-logger exclusion, per-request log buffering and release, the missing-handler warning at activation, and `NewSlogHandler` in Chi. |
| 7. Gin and Echo | Create `gin-v1`, `echo-v4` and `echo-v5` with `Init`, middleware, error dispatch, route listing, the full wrapper set, the Gin writer and the late-`Init` diagnostic for Gin. Add the modules to the Makefile and CI. |
| 8. Fiber | `internal/fasthttp.go`, tested from the Fiber modules. Create `fiber-v2` and `fiber-v3` with completion via the user-value closer, stream observation, `OnListen` activation outside prefork, shutdown-hook flush, error dispatch, route listing, wrappers and the late-`Init` diagnostic. Add the modules to the Makefile and CI. |
| 9. Release readiness | Add `GoLanguage` to `../sdk-tests/harness/languages.py`, a Go `core` module and the six apps with manifests and floor/latest variants plus a Fiber prefork variant. The apps use `signal.NotifyContext` with `Shutdown`. Run the harness against the local cloud stack when it is available. Leave the sdk-tests changes uncommitted in that repository. Rewrite `README.md` (quickstart, graceful shutdown section, logging, configuration) and add `MIGRATION.md` covering the migration contract in design section 13. Remove the version `sed` from `publish.yaml`. Keep `pocs/`: design section 17 links to it, so it is removed together with `docs/` when the design documents are retired, outside this plan. |

## 9. Testing

The rules in `AGENTS.md` apply. This section fixes where tests live and what each layer owns.

**Root module (`internal`).** Owns core semantics through `net/http` test apps built from `internal.NetHTTPMiddleware` and driven by `httptest`, plus direct tests of self-contained files. Areas, each in the test file of the source it pins:

- Configuration: precedence, disable controls, token masking, sample-rate fallback, pattern compilation and invalid-pattern drop, first-configuration-wins comparison.
- Runtime: activation once, test-binary suppression, startup event first and once, `Shutdown` deadline returning the context error, idle final drain delivering the uptime gauge.
- Providers: attach to a global SDK provider, own provider when unset, private provider for foreign and noop providers, propagator registration only together with Apitally's own global provider, and only when unset.
- Requests and span processing: root designation, reuse of an outer SERVER span, nested requests, duplicate SERVER spans exported as INTERNAL, release in both completion orders, late telemetry dropped, per-request caps, exclusions before sampling, deterministic sampling, callback abstain, panic and invalid-rate behavior.
- Capture and redaction: content-type allowlist, cap sentinel, incomplete bodies omitted, gzip and deflate, Brotli skipped, body mask results, query, header and body-field redaction, flushing and hijacking through the writer wrapper, `sendfile` preserved when not capturing.
- Errors, validation and consumers: first-error guard, recorded-500 rule, exception fields and stack format, validator recognition, aggregate bounds and drains, consumer normalization, attribute ordering, merge and LRU.
- Logs: request linkage, `MaskLogRecord` edits and drops, value conversion, truncation by rune, request-logger exclusion, SDK diagnostics not captured.
- Metrics: exponential bucket mapping including exact powers of two, delta collections, capacity, splitting at 1,000 combinations, process gauges.
- Export: spool rotation, caps, eviction order, memory fallback, retention, orphan cleanup, byte-identical retries, response classification, interval header clamping, send budget. Timer-driven tests run in `testing/synctest` bubbles.

**Framework modules.** Own integration behavior. Every module runs the canonical scenario set with identical names, in this order, following the Python suite's names where they apply:

1. `TestRequestExportsSingleServerSpanWithStableSemconv`
2. `TestClientAddressUsesFrameworkResolvedClientIP`
3. `TestHistogramAttributesAndLogCorrelation` (the handler logs with the context from design section 13's request helpers table)
4. `TestRouteIncludesGroupPrefix`
5. `TestStartupEventPathsMatchRoutes`
6. `TestRequestAndResponseBodiesCapturedAndRedacted`
7. `TestStreamingResponseSizeAndBodyCaptured`
8. `TestUnmatchedRequestHasNoRouteAndNoHistogramPoint`
9. `TestSetConsumerReachesSpanAndHistogram`
10. `TestUnhandledPanicRecordedOnServerSpan`
11. `TestValidationErrorReported`
12. `TestPreInstrumentedAppAdaptsWithoutDuplicateSpans`
13. `TestInitTwiceDoesNotStackMiddleware`
14. `TestDisabledSDKLeavesResponsesUnchanged`

`chi-v5` adds each scenario in the stage that implements its behavior (stages 3 to 6), keeping this order. The Gin, Echo and Fiber modules add the full set when they are created.

Framework-specific tests follow the canonical set: Gin recovery ordering and `WriteString`, Echo and Fiber single error-handler dispatch, Gin and Fiber late-`Init` error, Fiber `OnListen`, prefork skip, stream completion and abort, and shutdown-hook flush. The Fiber modules also own the `internal/fasthttp.go` tests: the root module never imports fasthttp, and each Fiber module's CI jobs run them at its fasthttp floor and latest version (stream wrapper active, sizes from stream types, capture of unknown-length streams). `chi-v5/sdk_test.go` asserts that `CaptureError` records the caller's file; the wrapper is identical in every module.

**Isolation.** Tests that touch the runtime, OTel globals, `slog.Default` or env vars call `internal.SetUpTest(t)` and the `testutils` helpers they need, which register their resets with `t.Cleanup`, and never call `t.Parallel`. Root tests build their `net/http` apps themselves. Assertions decode OTLP payloads received by the stub endpoint after `Shutdown`, with exact counts.

## 10. Build, CI and release

- **Makefile:** `check` runs build, `go vet`, `test -z "$(gofmt -l .)"`, `go mod verify` and `go mod tidy -diff` in the root module and every framework module, so formatting and `go.mod` drift fail instead of being rewritten; `test` runs `go test -race` in each. `pocs/` modules are never included.
- **CI (`tests.yaml`):** one matrix over modules and {floor on Go 1.25, latest on Go 1.26, latest on Go 1.27}, with `GOTOOLCHAIN=local`. "Latest" runs `go get -u -t ./...`, then `go mod tidy`, before testing. Plain `go get -u` would only update the root package's imports and miss `otlptrace`, the OTLP proto module, gopsutil and testify. Coverage upload to Codecov stays. Remove `v1` from the push trigger when the branch replaces `main`.
- **Publish (`publish.yaml`):** rewrite each framework module's root requirement and tag every framework module directory; the GitHub release tag is the root module's tag. The existing `*/go.mod` loop picks up `gin-v1` automatically. Releases start with `v1.0.0-beta.1` once the sdk-tests harness passes, matching the other SDKs.
