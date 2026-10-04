# Apitally Go v1 design

Status: Decided, 2026-10-04, pending the POCs in section 17.

This document adapts the shared SDK design to Go, the OpenTelemetry Go SDK and the supported web frameworks. It records Go decisions and the facts behind them. It is not an implementation plan.

## Sources

The [shared specification](../../cloud/docs/sdks/spec.md) owns the ingestion contract. The [shared design](../../cloud/docs/sdks/design.md) owns the cross-SDK architecture. Sections 1-16 below follow the shared design's numbering. The Python SDK is the reference implementation; the JavaScript and .NET SDKs are further references for adaptations. Shared requirements apply unless this document records an explicit Go adaptation.

- **Confirmed:** a decided Go choice.
- **Inherited:** a shared requirement that transfers to Go without adaptation.

Facts below were checked in October 2026 against OTel Go v1.47.0 (first stable logs API/SDK), contrib v1.47.0 / v0.72.0, and Go 1.27.1.

## 1. Product shape

**Inherited:** v1 is an OpenTelemetry distribution shipped as a new major version of the existing modules. It configures the official OTel Go SDK and sends OTLP/HTTP protobuf directly to Apitally using a write token. No Hub support. Shared defaults apply (traces, metrics, request-scoped logs, error capture and response headers on; request headers and bodies off; `SampleRate` 1.0; `Env` `dev`).

**Confirmed module layout:** one repository with a root module and one module per framework major version, as in v0:

| Module path | Framework |
| --- | --- |
| `github.com/apitally/apitally-go` | Shared implementation (`internal/...`) and shared public types |
| `github.com/apitally/apitally-go/chi-v5` | Chi v5 |
| `github.com/apitally/apitally-go/echo-v4` | Echo v4 |
| `github.com/apitally/apitally-go/echo-v5` | Echo v5 |
| `github.com/apitally/apitally-go/fiber-v2` | Fiber v2 |
| `github.com/apitally/apitally-go/fiber-v3` | Fiber v3 |
| `github.com/apitally/apitally-go/gin-v1` | Gin v1 (renamed from `gin`) |

Moving from v0.x to v1.x needs no `/v2` import-path suffix. Each framework package is named `apitally`, so users import a single package.

**Confirmed rewrite approach:** remove v0 code on the `v1` branch and build v1 fresh, porting proven logic (route enumeration, JSON field masking, size counting, response writer wrapping) after review against the v1 contract.

**Confirmed version floor:** Go 1.25+, tested on Go 1.25, 1.26 and 1.27. Gin 1.12, Echo v4/v5 and Fiber v3 already require Go 1.25. Dependency minimums are the newest releases that still declare `go 1.25`: OTel Go v1.46.0 (v1.47.0 requires Go 1.26) and `go.opentelemetry.io/proto/slim/otlp` v1.11.0 (v1.11.1 requires Go 1.26). Go's minimal version selection lets applications on newer Go use newer OTel releases. The module `go` directive stays at 1.25 until a needed dependency raises it.

## 2. Integration with existing OpenTelemetry setups

**Inherited:** never replace a user-owned tracer provider; attach Apitally's span processor additively. Apitally's logger pipeline and metrics are private and never registered as OTel globals. One process identity (`service.instance.id`, random UUIDv4 per process) and one resolved environment across all signals. Resource built through the standard OTel resource mechanism (`OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`), with the four Apitally-owned keys merged on top. On a user-owned provider, override `service.instance.id` and `deployment.environment.name` on Apitally's export copies only.

**Confirmed fallback sampler (when Apitally owns the provider):** record SERVER spans subject to the request-stage `SampleRate` test (skipped when `SampleOnRequest` is set; always record when the remote parent is sampled), record children of recorded local parents, drop everything else.

**Confirmed attribute limits:** Apitally encodes spans itself, so OTel attribute limits only matter on the live span. When Apitally owns the provider, pin the span attribute value length limit to 65,536 with explicit span limits, which override `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` and `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT`.

**Fact:** OTel Go has no public API to tell whether the global tracer provider was set, and no sampler getter on `*sdktrace.TracerProvider`. `RegisterSpanProcessor` is concurrency-safe, but a processor registered while spans are in flight can receive `OnEnd` without `OnStart`.

**Confirmed provider selection at activation** (the first request, after the application's OTel setup in `main`):

| Global tracer provider | Behavior |
| --- | --- |
| `*sdktrace.TracerProvider` (type assertion) | Attach Apitally's span processor with `RegisterSpanProcessor`. The user's sampler governs request-log coverage. No sampler warning, because OTel Go exposes no sampler getter. |
| Unset (a probe span from the global has an invalid span context) | Create Apitally's provider with the fallback sampler and register it with `otel.SetTracerProvider`, so spans from application code and libraries using the global become request descendants. Also register the W3C TraceContext + Baggage propagator with `otel.SetTextMapPropagator`, because Go's default global propagator is a no-op. |
| Any other implementation | Warn once that Apitally receives SERVER spans without descendants, and create a private Apitally provider used only by the middleware, not registered globally. |

There is no provider configuration option. Applications that keep their provider out of the globals register it with `otel.SetTracerProvider`, standard Go OTel practice. Spans in flight at registration may reach `OnEnd` without `OnStart`; the in-flight map treats them as unknown and drops them.

## 3. Configuration

**Inherited:** precedence is explicit options, then `APITALLY_*` env vars, then defaults. Env vars: `APITALLY_WRITE_TOKEN`, `APITALLY_ENV`, `APITALLY_DISABLED`, `APITALLY_OTLP_ENDPOINT` (testing only). Disable controls are additive (`Disabled`, `APITALLY_DISABLED`, `OTEL_SDK_DISABLED`; truthy `1`/`true`/`yes`). Missing or invalid token logs an error with a masked prefix and disables the SDK. `OTEL_EXPORTER_OTLP_*` and sampler env vars never affect Apitally. Invalid `SampleRate` resolves to 1.0. User patterns extend defaults; default patterns are case-insensitive.

**Confirmed environment resolution:** explicit `Env`, then `APITALLY_ENV`, then `dev`. No framework fallback: `GIN_MODE` and similar are run modes, not deployment environments.

**Confirmed process-global runtime:** one Apitally runtime per process, like Python and JavaScript, and like OTel Go's own globals. The first setup call's configuration wins; a later call with identical configuration is silent, a different one warns and is ignored. Every setup call still returns working middleware for the app it is given, so several routers in one process share one runtime. Because Go funcs are not comparable, configurations are compared through the startup event's `config` representation, in which callbacks appear only as `true`. The startup event's `config` keys are the Go field names (`CaptureLogs`, `SampleRate`).

**Confirmed configuration representation:** a flat `Config` struct in the root package, created with `NewConfig()`, which fills in the defaults that are not Go zero values (`CaptureLogs`, `CaptureResponseHeaders`, `SampleRate`). Each framework package re-exports it with a type alias and a `NewConfig` variable, so one import suffices:

```go
cfg := apitally.NewConfig()
cfg.WriteToken = "apt_..."
cfg.SampleRate = 0.5
r.Use(apitally.Middleware(r, cfg))
```

This keeps v0's shape and the config-struct convention of Go framework middleware, with option names matching the other SDKs. Only `WriteToken`, `Env` (empty means absent) and `Disabled` (additive) have env var fallbacks, so no absent-versus-default tracking is needed. Documentation always starts from `NewConfig()`; there is no runtime guard against struct literals.

**Confirmed pattern options:** `MaskQueryParams`, `MaskHeaders`, `MaskBodyFields` and `ExcludePaths` are `[]string` of Go RE2 patterns, matched by regex search and compiled case-insensitively by prepending `(?i)`, as in Python and .NET. Inline flags such as `(?-i:...)` opt out. Invalid patterns are dropped individually with an error log at configure time; the startup event lists the user's strings as given.

## 4. Lifecycle

**Confirmed test-suppression guard:** `testing.Testing()` (standard library, Go 1.21+) reports whether the binary is a `go test` binary. When true, the SDK never activates. The SDK's own tests bypass the guard through an internal hook.

**Confirmed:** no fork handling (Go processes do not fork after start) and no per-request context reset (Go passes `context.Context` explicitly, so request contexts cannot leak between requests).

**Inherited:** activation is attempted at most once per process; failure logs an error and the process serves untelemetered. At shutdown, requests not yet released are discarded; released requests go out in the final drain.

**Confirmed activation trigger:** the first request through the Apitally middleware, plus Fiber's `app.Hooks().OnListen` on Fiber v2/v3. Gin, Chi and Echo v4 have no startup hook, and Echo v5's listener callbacks exist only on `StartConfig`, which many apps do not use. Activation runs inside a `sync.Once`, so concurrent first requests wait for it to complete and the first request's SERVER span enters the activated pipeline. At that point all routes are registered (Chi panics on `Use` after routes; Gin and Fiber apply middleware only to later routes, so `Middleware` is necessarily called before routes are added), so the startup event's `paths` are complete without a delay heuristic, and the application's own OTel setup in `main` has already run. Load balancer and Kubernetes probes pass through the middleware and activate the SDK; they are excluded from traces but not from activation. Accepted consequence: an instance that receives neither traffic nor probes is not reported online.

**Confirmed shutdown:** a public `apitally.Shutdown(ctx context.Context) error` in the root package, re-exported by each framework package, runs the final export cycle within the context's deadline and returns the context error if the deadline expires before delivery completes, like `http.Server.Shutdown` and `TracerProvider.Shutdown`. This is the Go counterpart of .NET's use of the host shutdown budget. It shuts down Apitally's own tracer provider, never a user-owned one. On Fiber v2/v3 it is also called automatically from the app's shutdown hooks, which run synchronously within `app.Shutdown`. Documentation shows it next to `srv.Shutdown(ctx)` in the standard `signal.NotifyContext` pattern, and the migration guide calls it out.

No other trigger exists. Go has no exit hook (returning from `main` ends all goroutines, and SIGTERM terminates without running deferred code). The SDK does not intercept termination signals: `signal.Notify` disables Go's default SIGTERM termination process-wide, and the SDK cannot tell whether the application has its own handler, so it could neither safely re-raise the signal (cutting short an application's graceful drain) nor safely refrain (leaving a process without a handler unable to exit). `http.Server.RegisterOnShutdown` callbacks run in goroutines that `Shutdown` does not wait for, so they cannot flush. Accepted consequence: applications that never call `Shutdown` lose the telemetry not yet delivered at exit.

## 5. Request model: span filtering and exclusion

**Inherited:** request-rooted traces only. Classification at span start into an in-flight request map keyed by span id, inheriting the SERVER span id downward. Exclusions (`OPTIONS`, websocket upgrades, default and user path patterns, default user-agent patterns) run before sampling. A SERVER span with a local parent (stacked HTTP-server instrumentation) inherits its parent's entry and is exported as INTERNAL, with a once-per-scope warning.

**Confirmed request data source:** exclusion matching and SERVER span enrichment read the framework's request data (method, path, `User-Agent`, route), not span attributes, as in .NET. No old-semconv normalization is needed at span start, including on reused user-owned SERVER spans. Query redaction at export still covers stable and legacy query-bearing attributes on all spans, including those from user instrumentation.

**Confirmed websockets:** requests carrying `Upgrade: websocket` are excluded from traces, metrics and error capture. Go websocket libraries hijack the connection, and no Go instrumentation in scope emits per-message spans, so no per-message span filter exists.

**Fact:** Go has no implicit context. Descendant spans and logs are linked to a request only when application code passes the request context (`r.Context()`, or the framework equivalent) to tracers and loggers. This is standard Go OTel practice and is documented, not worked around.

## 6. Sampling and per-request buffering

**Inherited:** two-stage sampling with the deterministic trace-ID ratio test; callbacks fail open with a warning; per-request buffers of 1,000 spans and 1,000 log records; release once both transport observation and SERVER span end have completed; release order descendants, SERVER span, logs.

**Confirmed completion coordination:** when the middleware created the SERVER span, it ends the span only after transport observation has completed (final status, sizes, route and error state known), so transport completion always precedes span end and response sampling runs at span end. When it reused a user-owned SERVER span, the user's instrumentation ends that span after the middleware returns; release waits for both events, as the shared design requires.

**Confirmed late telemetry:** spans and logs that arrive after their request is released are dropped locally, as in .NET. In Go these come from goroutines that outlive the request while holding its context. No completed-request cache.

**Confirmed callback span type:** all four span callbacks receive OTel Go's native `sdktrace.ReadOnlySpan`, as Python and JavaScript pass their native `ReadableSpan`. `SampleOnRequest` receives the live SERVER span at start. `SampleOnResponse` and the body-mask callbacks receive Apitally's export copy: a struct embedding the original `ReadOnlySpan` (embedding satisfies the interface's unexported method, the technique OTel's own `tracetest` snapshots use) that overrides `Attributes()` and `Resource()`, and `SpanKind()` for the duplicate SERVER rule in section 5. User exporters never see the copy. To be verified by a POC.

**Confirmed sampling callbacks:** `SampleOnRequest` and `SampleOnResponse` are `func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)`. `ok == false` abstains: the request stage falls back to `SampleRate`, the response stage keeps the earlier decision. A rate outside [0, 1], NaN or a panic warns and keeps (fail open). Keep and drop are `1, true` and `0, true`; the comma-ok result is Go's idiom for an optional value.

## 7. Capture pipeline

**Confirmed body-mask callbacks:** `MaskRequestBody` and `MaskResponseBody` are `func(span sdktrace.ReadOnlySpan, body []byte) []byte`, span first as in Python and .NET. They receive the decompressed body and return the replacement; a nil or empty result or a panic yields `[REDACTED]`, and an over-cap result yields `[BODY_TOO_LARGE]`. They run on the span batch processor's export goroutine against the export copy.

**Inherited:** header-only capture decisions, content-type allowlist, 50,000-byte cap with `[BODY_TOO_LARGE]`, complete bodies only, payloads invisible to user exporters, redaction before export, fail closed. Processing order: bounded decompression, mask callback, JSON parse, field redaction, compact serialization.

**Confirmed decompression:** `gzip` and zlib-wrapped `deflate` via the standard library. The standard library has no Brotli decoder, so `br` bodies are skipped (attribute absent), as the spec allows.

**Confirmed processing location:** decompression, masking and redaction run on the span batch processor's export goroutine, never on request-serving goroutines.

**Confirmed transport mechanics:**

- **net/http frameworks (Chi, Echo, Gin):** wrap `Request.Body` and the `http.ResponseWriter` with bounded capture and byte counting, passing through optional interfaces via `Unwrap` for `http.ResponseController`. The request body wrapper captures only bytes the application reads and never reads more itself; a captured request body is complete when EOF is observed. Request size comes from `Content-Length`, else from the byte count at EOF; a body the application never reads to EOF has no determinable size without `Content-Length`.
- **Fiber (fasthttp):** request and ordinary response bodies are already buffered in memory by fasthttp, so capture copies at most 50,000 bytes after the handler. Not captured: request bodies with Fiber's `StreamRequestBody` enabled and streamed responses (`SetBodyStreamWriter`). fasthttp reuses request memory after the handler returns, so all captured values are copied before then.

## 8. Transport observation, routes, frameworks

**Inherited:** the transport layer observes every response within the monitored scope, including framework error-handler responses. `http.route` is the parameterized template including group and mount prefixes; unmatched requests carry no route. Client address follows the framework's own resolution (`c.ClientIP()`, `c.RealIP()`, `c.IP()`, `r.RemoteAddr`), never SDK-parsed forwarding headers.

**Confirmed monitored scope:** the whole application the middleware is registered on. The Apitally middleware must be registered first (outermost), before routes; this is documented per framework.

**Confirmed SERVER span creation:** the Apitally middleware creates the SERVER span on all six integrations, as the shared design permits; no stock framework instrumentation is used. Stock Go instrumentation is uneven: `otelgin` and Fiber v3's `gofiber/contrib/v3/otel` are adequate, but contrib `otelecho` was removed in October 2026 in favor of the non-semver `labstack/echo-otel`, `otelfiber/v2` mixes old and stable semconv, `otelchi` uses old semconv, and `otelhttp` reads `http.route` only at span start, omits `url.query`, and derives `client.address` from `X-Forwarded-For` itself. The middleware:

- extracts upstream context with the global propagator, falling back to W3C TraceContext;
- sets stable HTTP semconv attributes from the framework's own request data (method, route template, scheme, host, path, query, client IP, status, sizes);
- names the span `{method} {route}`, or `{method}` without a route;
- puts the span into the request context (`r.Context()`, Fiber's user context) so handler spans nest under it;
- uses the framework module path (e.g. `github.com/apitally/apitally-go/gin-v1`) as instrumentation scope.

When the incoming request context already holds a recording SERVER span, because the application installed OTel HTTP instrumentation outside Apitally's middleware, the middleware reuses that span and creates none. Instrumentation installed inside Apitally's middleware produces a SERVER span with a local parent, handled by the duplicate rule in section 5.

### Server errors

**Inherited:** request-local error state keeps the first captured error; a server error counts only with a captured error and final status 500; captured errors become the SERVER span's `exception` event (first only); cancellation is never captured.

**Confirmed automatic capture sources:**

- **Panics** (all frameworks): recovered in the middleware's deferred function, captured, then re-panicked so the application's or framework's recovery middleware still writes the response. A panicking request counts as status 500. `gin.Default()` registers Recovery before Apitally's middleware, so it sits outside.
- **Echo and Fiber:** errors returned by the handler chain. The middleware invokes the framework's error handler itself (`c.Error(err)` in Echo, the app's `ErrorHandler` in Fiber), so it observes the final status. v0 read the status before the framework's error handler ran.
- **Gin:** the first entry in `c.Errors` (`c.Error`, `c.AbortWithError`).
- **Chi:** panics and explicit `CaptureError` only; net/http has no error channel.

The final-500 rule filters all sources, so for example `echo.NewHTTPError(400)` contributes nothing. Errors matching `errors.Is(err, context.Canceled)` and the `http.ErrAbortHandler` panic are not captured.

**Confirmed exception fields:**

- `type`: the Go type name of the captured value with one pointer level removed (`fs.PathError`, `pq.Error`), as in v0; `string` for `panic("boom")`.
- `message`: `err.Error()`, or `fmt.Sprint(v)` for non-error panic values.
- `stacktrace`: built from `runtime.Callers` and formatted deterministically as `function\n\tfile:line` per frame, without goroutine IDs, arguments or `+0x` offsets, so the displayed value is also a stable aggregation key. Panics use the stack taken inside the deferred recover (which includes the panicking frames), excluding Apitally's own frames. Explicit `CaptureError` uses the stack at its call site. Automatically captured returned errors and `c.Errors` entries have an empty stacktrace: Go errors carry no stack, and the middleware's own stack is meaningless.

### Validation errors

**Confirmed explicit deviation from the shared spec:** Go frameworks do not own validation responses; handlers typically validate and write their own 400 (Gin's `c.ShouldBind*`, Chi, Fiber v2), which no SDK mechanism can observe. Validation capture is therefore automatic where the framework sees the error, plus a public `CaptureValidationError(c/ctx, err)` helper, kept from v0, alongside the other request helpers in section 13.

- **Automatic:** errors in the framework error channels from the server error section (Gin `c.Errors` from `c.Bind*`, errors returned to Echo and Fiber, including Echo's `c.Validate` and Fiber v3's `c.Bind()` with a struct validator) when the final status is 400 or 422.
- **Explicit:** `CaptureValidationError` records the error's details in request-local state; they are committed at transport completion under the shared eligibility rules (routed, not `OPTIONS`), regardless of status.

**Confirmed recognition:** `go-playground/validator/v10` errors, the de facto Go validator (used by Gin's binding), recognized by method set rather than by import: a slice in the error's `Unwrap` chain whose elements have `Namespace()`, `Field()`, `Tag()` and `Error()`. This avoids adding the validator's dependency tree to modules whose users do not use it. Other errors are ignored. Each element maps to:

- `field`: `Namespace()` without the leading struct name (`Address.City`, or `address.city` with a registered JSON tag-name function), preserving the validator's own path string;
- `type`: `Tag()` (`required`, `email`);
- `message`: `Error()`;
- `source`: empty, because the bind source is unknown to the SDK.

## 9. Logs

**Inherited:** request-scoped application logs only, linked via `apitally.request.server_span_id`; records without a request are dropped; SDK and OTel diagnostics are never captured; `CaptureLogs=false` disables only application-log capture; bodies truncated to 2,048 characters after masking.

**Confirmed startup event:** `paths` is the union of the routes of all apps passed to `Middleware` before activation, enumerated with the framework's route listing (`gin.Engine.Routes`, `echo.Echo.Routes`, `fiber.App.GetRoutes`, `chi.Walk`), omitting `HEAD`, `OPTIONS` and catch-all method registrations. `framework` is `chi`, `echo`, `fiber` or `gin`. `versions` contains `go` (`runtime.Version()`), the framework module version from `debug.ReadBuildInfo()`, and `app` when configured. `openapi` is omitted: none of the supported frameworks produces an OpenAPI document natively.

**Inherited:** error aggregation (100 + 100 distinct errors, per-consumer counts, drained before log flushes) and consumer-update events (10,000-entry LRU of normalized payload hashes).

**Confirmed capture surface:** `log/slog` only; zap, zerolog and logrus are out of v1 scope.

- **Automatic:** at activation, wrap the handler of `slog.Default()` with Apitally's capture handler and install it with `slog.SetDefault`. Skipped when the default handler is already an Apitally handler.
- **Explicit:** `apitally.NewSlogHandler(next slog.Handler) slog.Handler` (root package, re-exported) for loggers not derived from `slog.Default()` at activation, such as loggers built with `slog.New(h)` and injected, or a `slog.Default()` stored before activation.
- **Unchanged output:** when the default handler is the standard library's original one, `slog.SetDefault` would redirect the `log` package into the wrapper while the original handler writes through the `log` package, creating a loop. After installing the wrapper, the SDK restores the `log` package's original writer and flags, so both `slog` and `log` output stay identical to before. To be verified by a POC.

Records are linked to a request only when the request context is passed (`slog.InfoContext(ctx, ...)`, `logger.Log(ctx, ...)`). Records without one, including all `log` package output, are dropped. `slog.SetDefault` calls after activation replace the automatic wrapper; first-request activation makes this unlikely. The capture handler forwards every record to the wrapped handler unchanged and reports `Enabled` as the wrapped handler does, so user-configured levels apply.

**Confirmed record content:** the body is the slog record's `Message`. All attributes, including those added through `Logger.With` and `WithGroup`, are exported as OTel log record attributes, with groups as nested map values and slog value kinds mapped to OTLP values following the `otelslog` bridge's conversion. String attribute values are truncated to 2,048 characters, like the body. The instrumentation scope name is `slog` (spec section 8); `code.file.path`, `code.line.number` and `code.function.name` come from the record's `PC`. The server currently stores only message, level, logger and code location; exporting attributes now means they become visible as soon as the server stores them, without an SDK change.

**Confirmed record pipeline (adaptation, as in .NET):** no OTel log SDK or logger provider. The capture handler builds an internal record, buffered per request under section 6; released records go to a log batching goroutine with a ~1s schedule that encodes and appends them to the logs spool. SDK internal events (startup, error aggregates, consumer updates) are built directly as internal records with scope `apitally`, their event name and no trace context, and take the same path, bypassing masking and truncation. OTel Go's log SDK is pre-1.0 (`sdk/log` v0.22.0) at the Go 1.25 floor, a module path that applications may resolve to an incompatible newer version; only its batch processor would be used, since the exporters' protobuf conversion is internal and its records can only be created through an OTel `Logger`.

**Confirmed `MaskLogRecord`:** `func(record slog.Record) (slog.Record, bool)`, using Go's own log record type. It runs synchronously in the capture handler, before request buffering, on a fresh record that already includes the logger's `With`/`WithGroup` attributes, so it sees exactly what will be exported and cannot affect the application's own log output. Returning `(r, true)` exports the returned record; `false` or a panic drops it. Because `slog.Record` is a value type, returning a modified copy is the ordinary way to modify it, so the shared rule that a replacement record drops does not apply.

## 10. Export pipeline

**Inherited:** stock batch processor intake with explicit settings and ~1s delay; gzip-appended per-signal spool files with 4 MB rotation; one export worker with jittered cycles, send budget, retry classification, 59-minute retention, 50 MB disk / 10 MB memory caps, filesystem probe with memory fallback, 2-hour orphan cleanup; final drain on shutdown.

**Confirmed encoding:** official `go.opentelemetry.io/proto/slim/otlp` generated types with `google.golang.org/protobuf`. The regular `go.opentelemetry.io/proto/otlp` collector packages pull in gRPC and grpc-gateway; the slim module does not. The OTel exporters' SDK-to-protobuf conversions are internal, so Apitally owns a conversion matching them.

**Confirmed HTTP client:** a private `http.Client` with its own `http.Transport` (never `http.DefaultTransport`, which users may replace with instrumented transports) and `Proxy: http.ProxyFromEnvironment`, which reads proxy env vars once per process. OTel Go has no instrumentation-suppression context; the private transport is uninstrumented, so export requests produce no spans.

**Confirmed spool files:** `os.CreateTemp` in `os.TempDir()`, which creates files with `0600` permissions.

**Confirmed background goroutines:** span batch processor, log batching and export worker; three, matching the reference implementation.

## 11. Metrics

**Inherited:** own aggregation of the three delta exponential histograms at fixed scale 3 (no OTel metrics SDK), 50,000 combinations per interval with a once-per-process warning, collection driven by the export worker, process gauges observed at collection.

**Confirmed process gauges:** `github.com/shirou/gopsutil/v4`, as in v0, for CPU time and RSS on all platforms without cgo. Go's standard library provides process CPU time everywhere (`syscall.Getrusage`, `syscall.GetProcessTimes`) but current RSS only on Linux (`/proc/self/statm`). CPU utilization is the CPU-time delta divided by elapsed time and `runtime.NumCPU()`, clamped to [0, 1], consistent with the CPU counts the other SDKs use. The minimum is v4.26.8, the newest release declaring `go 1.24` (v4.26.9 requires Go 1.26).

## 12. Error handling and logging posture

**Inherited:** SDK failures never break the app; quiet by default; deduplicated actionable warnings; never log the raw write token.

**Confirmed:** SDK diagnostics use a private `*slog.Logger` writing to stderr, which the log capture never sees.

## 13. Public API

### Setup

`Middleware(app, cfg *Config)` in each framework package returns the framework's middleware type (`gin.HandlerFunc`, `echo.MiddlewareFunc`, `fiber.Handler`, `func(http.Handler) http.Handler`), registered first with the framework's `Use`. The app argument (`*gin.Engine`, `*echo.Echo`, `*fiber.App`, `chi.Router`) is used for route enumeration and, on Fiber, lifecycle hooks. Configuration is described in section 3, shutdown in section 4.

Each framework package re-exports from the root package: `Config`, `NewConfig`, `Consumer`, `Shutdown`, `NewSlogHandler` and `StartSpan`, plus framework-context wrappers of the request helpers below.

### Request helpers

The middleware stores a pointer to per-request state (consumer, SERVER span handle, error state, request attributes) in the request's `context.Context`. Helpers look it up there and are silent no-ops outside a monitored request or when Apitally is disabled. The root package holds the implementation as `context.Context` helpers; each framework package exports wrappers with the same names that take the framework's own context:

| Framework package | Root package |
| --- | --- |
| `SetConsumer(c, Consumer)` | `SetConsumer(ctx, Consumer)` |
| `SetRequestAttributes(c, attrs ...attribute.KeyValue)` | `SetRequestAttributes(ctx, attrs ...attribute.KeyValue)` |
| `CaptureError(c, err)` | `CaptureError(ctx, err)` |
| `CaptureValidationError(c, err)` | `CaptureValidationError(ctx, err)` |

The framework context is `*gin.Context`, `echo.Context` (v4) / `*echo.Context` (v5), `*fiber.Ctx` (v2) / `fiber.Ctx` (v3), and `*http.Request` for Chi. Handlers use the framework package; service code that only has a `ctx` uses the root package. A Gin-specific wrapper is required because `*gin.Context` implements `context.Context` but resolves non-string keys from the request context only when `ContextWithFallback` is enabled, which is off by default.

`Consumer` is `struct { Identifier, Name, Group string; Attributes map[string]string }`. An empty attribute value deletes the key (spec section 9.3). `SetRequestAttributes` takes OTel's native `attribute.KeyValue`, variadic like `span.SetAttributes`; it is the Go form of `set_request_attribute`. `CaptureError` uses Go's error vocabulary for `capture_exception`.

The Apitally middleware must be outermost, so state exists before any user middleware runs; a consumer set by middleware registered before Apitally's is lost.

### Manual tracing

**Confirmed:** `StartSpan(ctx, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span)` in the root package, re-exported by the framework packages. It creates an INTERNAL span under tracer scope `apitally.otel` from the global tracer provider and adds the caller's `code.function.name`, `code.file.path` and `code.line.number` (`runtime.Caller`). It returns native OTel types; `defer span.End()` is Go's block form. There is no function wrapper: Go has no decorators, and closure-based wrappers are unusual Go. Outside a monitored request the span is not recorded by Apitally's fallback sampler.

```go
ctx, span := apitally.StartSpan(ctx, "search_books")
defer span.End()
```

No contrib setup wrappers: Go instrumentations are already one-liners (`otelhttp.NewTransport`, `otelsql.Open`, `redisotel.InstrumentTracing`); documentation shows them.

### Migration contract

The migration guide covers: `ClientID` replaced by `WriteToken` (and `APITALLY_WRITE_TOKEN`); the nested `RequestLoggingConfig` flattened into `Config` with the shared `Capture*` names; request logging and log capture on by default; `ExcludeCallback` replaced by the keep-probability sampling callbacks; `SetConsumerIdentifier` and the `ApitallyMiddleware`/`ApitallyConfig`/`ApitallyConsumer` aliases removed; `Consumer.Attributes` added; the `gin` module renamed to `gin-v1`; the new `Shutdown` call; `slog.InfoContext` with the request context required for log linkage; the Go 1.25 floor.

## 14. Sentry integration

**Confirmed scope deviation:** deferred beyond v1, as in .NET; v0 Go had none. Ordinary error capture and aggregation are unaffected. Go cannot detect an optional dependency without importing it, so automatic detection would put `sentry-go` into every user's build. The intended future path is a separate opt-in module (`github.com/apitally/apitally-go/sentry`) registering a `sentry-go` event processor, added without changing existing APIs.

## 15. Cross-language posture and explicit adaptations

Wire attributes, scope names, default redaction and exclusion patterns, the sampling convention, body privacy rules, error identities and limits, buffer caps, spool rules and delivery behavior remain shared requirements. Go adaptations:

| Shared design area | Go treatment |
| --- | --- |
| Setup entry point | Per-framework `Middleware(app, cfg)` with a flat `Config` from `NewConfig()`; no unified detecting entry point, since each framework is a separate module. |
| Runtime ownership | Process-global, first configuration wins, as in Python and JavaScript. |
| SERVER spans | Created by Apitally's middleware on all frameworks; no stock framework instrumentation. User-owned outer SERVER spans are reused. |
| Tracer provider | Attach to a global SDK provider; otherwise register Apitally's provider and the W3C propagator as globals; private SERVER-only provider for foreign providers. No provider option. |
| Activation | First request, plus Fiber `OnListen`; `testing.Testing()` suppresses activation. |
| Shutdown | Explicit `Shutdown(ctx)`, automatic only via Fiber shutdown hooks. No exit hook or signal handling. |
| Request helpers | Explicit framework context or `context.Context` arguments instead of implicit request context. |
| Validation capture | Automatic recognition of `go-playground/validator` errors in framework error channels plus a public `CaptureValidationError`; explicit deviation from the spec's no-API rule. |
| Exceptions | Go errors and panics; Go type name without pointer; deterministic `runtime.Callers` stacktraces; empty stacktrace for automatically captured returned errors. |
| Sampling callback result | Comma-ok `(rate float64, ok bool)`. |
| Span callback type | Native `sdktrace.ReadOnlySpan`; export copy embeds the original span. |
| Logs | `log/slog` only; default handler wrapped automatically plus `NewSlogHandler`; linkage requires the request context; attributes exported as log attributes. |
| Log pipeline and callback | Apitally-owned records without OTel log SDK, as in .NET; `MaskLogRecord` uses `slog.Record` with value semantics. |
| Pattern options | `[]string`, case-insensitive by default, inline flags respected, as in Python and .NET. |
| Brotli | Not decoded (no standard library decoder); `br` bodies are not captured. |
| Process gauges | `gopsutil/v4`. |
| Manual tracing | `StartSpan(ctx, name)` returning native OTel types; no function wrapper, no contrib wrappers. |
| OpenAPI | Omitted from the startup event. |
| Sentry | Deferred beyond v1. |
| Late telemetry | Dropped after release, as in .NET. |
| Fork and context isolation | Not applicable. |

## 16. Code style and testing

**Confirmed:** standard `go test` with `-race`, `testify` as in v0, `httptest` servers driving small real apps per framework. Assertions run against decoded OTLP payloads captured by a local stub endpoint or an in-process spool reader. No test doubles for Apitally's own code. Integration tests follow the Python suite's scenario names where they apply, and share names across the Go framework modules.

Add a Go language adapter and Go bookstore apps to the sibling [SDK test harness](../../sdk-tests/README.md), which stops apps with SIGTERM: the Go apps use the standard `signal.NotifyContext` graceful-shutdown pattern with `Shutdown`.

## 17. POCs

To be built in `pocs/` before the implementation plan relies on them:

1. **Export span copy:** a struct embedding `sdktrace.ReadOnlySpan` with overridden `Attributes()`, `Resource()` and `SpanKind()` passes through `BatchSpanProcessor` and is accepted where `ReadOnlySpan` is required, while user exporters receive the unmodified span.
2. **slog default wrapping:** wrapping the standard library's original default handler and restoring the `log` package's writer and flags keeps `slog` and `log` output byte-identical and causes no loop.
3. **Provider detection:** the type assertion and probe span distinguish an SDK provider, an unset global and a foreign provider on OTel Go v1.46 and v1.47.
4. **Framework error handlers:** invoking Echo's and Fiber's error handlers inside the middleware yields the final status without writing the response twice, and Gin `c.Errors` and panics behave as described in section 8.

