# Apitally Go v1 design

Status: Decided, 2026-10-05. POC findings are incorporated (section 17).

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

**Confirmed framework floors:** each framework module starts from its v0 minimum (Chi v5.1.0, Echo v4.11.4, Echo v5.0.4, Fiber v2.51.0, Fiber v3.0.0, Gin v1.9.1), as Python and JavaScript keep established floors. A floor is raised only when a v1 mechanism or behavioral check fails at it, such as fasthttp's error-aware stream close under Fiber, `ListenData.Prefork` on Fiber v3 or Gin's writer interface; the new floor and its reason are recorded here. CI runs every framework module at its floor and at the latest release.

## 2. Integration with existing OpenTelemetry setups

**Inherited:** never replace a user-owned tracer provider; attach Apitally's span processor additively. Apitally's logger pipeline and metrics are private and never registered as OTel globals. One process identity (`service.instance.id`, random UUIDv4 per process) and one resolved environment across all signals. Resource built through the standard OTel resource mechanism (`OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`), with the four Apitally-owned keys merged on top. On a user-owned provider, override `service.instance.id` and `deployment.environment.name` on Apitally's export copies only.

**Confirmed fallback sampler (when Apitally owns the provider):** record SERVER spans subject to the request-stage `SampleRate` test (skipped when `SampleOnRequest` is set; always record when the remote parent is sampled), record children of recorded local parents, drop everything else.

**Confirmed attribute limits:** Apitally encodes spans itself, so OTel attribute limits only matter on the live span. When Apitally owns the provider, pin the span attribute value length limit to 65,536 with explicit span limits, which override `OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT` and `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT`.

**Fact:** OTel Go has no public API to tell whether the global tracer provider was set, and no sampler getter on `*sdktrace.TracerProvider`. `RegisterSpanProcessor` is concurrency-safe, but a processor registered while spans are in flight can receive `OnEnd` without `OnStart`.

**Confirmed provider selection at activation** (the first request, after the application's OTel setup in `main`):

| Global tracer provider | Behavior |
| --- | --- |
| `*sdktrace.TracerProvider` (type assertion) | Attach Apitally's span processor with `RegisterSpanProcessor`. The user's sampler governs request-log coverage. No sampler warning, because OTel Go exposes no sampler getter. |
| Unset (identical to the value captured at package initialization) | Create Apitally's provider with the fallback sampler and register it with `otel.SetTracerProvider`, so spans from application code and libraries using the global, including tracers cached before activation, become request descendants. |
| Any other implementation, including an explicitly set `noop.TracerProvider` | Warn once that Apitally receives SERVER spans without descendants, and create a private Apitally provider used only by the middleware, not registered globally. Always create the monitored SERVER span with this provider; never reuse a foreign span. |

**Confirmed unset detection:** the root package captures `otel.GetTracerProvider()` and `otel.GetTextMapPropagator()` in package-level variables at initialization, before the application's `main` runs. At activation, a global identical to its captured value is unset; `otel.SetTracerProvider` and `otel.SetTextMapPropagator` replace the returned value. This uses public API only. A probe span is not used: it is exported by recording foreign providers, and noop providers and inherited request contexts make its span context an unreliable signal. The only misclassification is a foreign provider set in another package's `init` that Go initializes before Apitally's; setting providers in `init` is not a supported practice.

**Confirmed propagator:** register the W3C TraceContext + Baggage propagator with `otel.SetTextMapPropagator` only when the global propagator is also still unset, because Go's default global propagator is a no-op. An application-set propagator, including an empty one, is kept.

There is no provider configuration option. Applications that keep their provider out of the globals register it with `otel.SetTracerProvider`, standard Go OTel practice. Spans in flight at registration may reach `OnEnd` without `OnStart`; unrelated spans remain unknown and are dropped. A monitored request's reused SERVER span is explicitly associated at middleware entry, including when its start preceded processor registration (sections 5-6). An application that replaces the global provider after activation splits the pipelines: tracers obtained before the replacement stay bound to Apitally's provider. Apitally's batch processor exports only sampled span contexts, so spans a user's sampler marks record-only never reach Apitally.

## 3. Configuration

**Inherited:** precedence is explicit options, then `APITALLY_*` env vars, then defaults. Env vars: `APITALLY_WRITE_TOKEN`, `APITALLY_ENV`, `APITALLY_DISABLED`, `APITALLY_OTLP_ENDPOINT` (testing only). Disable controls are additive (`Disabled`, `APITALLY_DISABLED`, `OTEL_SDK_DISABLED`; truthy `1`/`true`/`yes`). Missing or invalid token logs an error with a masked prefix and disables the SDK. `OTEL_EXPORTER_OTLP_*` and sampler env vars never affect Apitally. Invalid `SampleRate` resolves to 1.0. User patterns extend defaults; default patterns are case-insensitive.

**Confirmed environment resolution:** explicit `Env`, then `APITALLY_ENV`, then `dev`. No framework fallback: `GIN_MODE` and similar are run modes, not deployment environments.

**Confirmed process-global runtime:** one Apitally runtime per process, like Python and JavaScript, and like OTel Go's own globals. The first setup call's configuration wins; a later call with identical configuration is silent, a different one warns and is ignored. Every setup call still instruments the app it is given, guarded against duplicate installation on that app, so several routers in one process share one runtime. Resolved configurations (after env var fallbacks) are compared field by field, as in JavaScript: callback fields are equal when both are set or both are unset, because Go funcs are not comparable and app factories create them inline; all other fields, including `WriteToken`, `Env` and `Disabled`, are compared with `reflect.DeepEqual` after clearing the callback fields. The warning names no values. The startup event's `config` keys are the Go field names (`CaptureLogs`, `SampleRate`).

**Confirmed configuration representation:** a flat `Config` struct in the root package, created with `NewConfig()`, which fills in the defaults that are not Go zero values (`CaptureLogs`, `CaptureResponseHeaders`, `SampleRate`). Each framework package re-exports it with a type alias and a `NewConfig` variable, so one import suffices:

```go
cfg := apitally.NewConfig()
cfg.WriteToken = "apt_..."
cfg.SampleRate = 0.5
```

This keeps v0's shape and the config-struct convention of Go framework middleware, with option names matching the other SDKs. Only `WriteToken`, `Env` (empty means absent) and `Disabled` (additive) have env var fallbacks, so no absent-versus-default tracking is needed. Documentation always starts from `NewConfig()`; there is no runtime guard against struct literals.

**Confirmed pattern options:** `MaskQueryParams`, `MaskHeaders`, `MaskBodyFields` and `ExcludePaths` are `[]string` of Go RE2 patterns, matched by regex search and compiled case-insensitively by prepending `(?i)`, as in Python and .NET. Inline flags such as `(?-i:...)` opt out. Invalid patterns are dropped individually with an error log at configure time; the startup event lists the user's strings as given.

## 4. Lifecycle

**Confirmed test-suppression guard:** `testing.Testing()` (standard library, Go 1.21+) reports whether the binary is a `go test` binary. When true, the SDK never activates. The SDK's own tests bypass the guard through an internal hook.

**Confirmed:** no fork handling (Go processes do not fork after start) and no per-request context reset (Go passes `context.Context` explicitly, so request contexts cannot leak between requests).

**Inherited:** activation is attempted at most once per process; failure logs an error and the process serves untelemetered. At shutdown, requests not yet released are discarded; released requests go out in the final drain.

**Confirmed activation trigger:** the first request through the Apitally middleware, plus Fiber's `app.Hooks().OnListen` on Fiber v2/v3 when prefork is disabled. With prefork, Fiber runs `OnListen` only in the master process, which never serves requests, so the hook skips activation when `app.Config().Prefork` (v2) or `ListenData.Prefork` (v3) is set. Prefork children are newly executed processes that run `main` and `Init` themselves, and activate on their first request. Gin, Chi and Echo v4 have no startup hook, and Echo v5's listener callbacks exist only on `StartConfig`, which many apps do not use. Activation runs inside a `sync.Once`, so concurrent first requests wait for it to complete and the first request's SERVER span enters the activated pipeline. At that point all routes are registered (Chi panics on `Use` after routes; Gin and Fiber apply middleware only to later routes, so `Init` is called before groups and routes are added), so the startup event's `paths` are complete without a delay heuristic, and the application's own OTel setup in `main` has already run. Load balancer and Kubernetes probes pass through the middleware and activate the SDK; they are excluded from traces but not from activation. Accepted consequence: an instance that receives neither traffic nor probes is not reported online.

**Confirmed shutdown:** a public `apitally.Shutdown(ctx context.Context) error` in the root package, re-exported by each framework package, runs the final export cycle within the context's deadline and returns the context error if the deadline expires before delivery completes, like `http.Server.Shutdown` and `TracerProvider.Shutdown`. This is the Go counterpart of .NET's use of the host shutdown budget. It shuts down Apitally's own tracer provider, never a user-owned one. `Shutdown` is the only terminal path. On Fiber v2/v3, the app's shutdown hooks, which run synchronously within `app.Shutdown`, run one non-terminal export cycle for released telemetry, as JavaScript does on server close; the runtime keeps serving other apps in the process. Fiber hooks receive no context, so the deadline passed to `ShutdownWithContext` does not apply; the hook flush has its own fixed 5-second deadline, matching JavaScript's signal flush. Files it does not deliver stay queued. Documentation shows it next to `srv.Shutdown(ctx)` in the standard `signal.NotifyContext` pattern, and the migration guide calls it out.

No other trigger exists. Go has no exit hook (returning from `main` ends all goroutines, and SIGTERM terminates without running deferred code). The SDK does not intercept termination signals: `signal.Notify` disables Go's default SIGTERM termination process-wide, and the SDK cannot tell whether the application has its own handler, so it could neither safely re-raise the signal (cutting short an application's graceful drain) nor safely refrain (leaving a process without a handler unable to exit). `http.Server.RegisterOnShutdown` callbacks run in goroutines that `Shutdown` does not wait for, so they cannot flush. Accepted consequence: applications that never call `Shutdown` lose the telemetry not yet delivered at exit.

## 5. Request model: span filtering and exclusion

**Inherited:** request-rooted traces only. Classification at span start into an in-flight request map keyed by span id, inheriting the SERVER span id downward. Exclusions (`OPTIONS`, websocket upgrades, default and user path patterns, default user-agent patterns) run before sampling. A SERVER span with a local parent (stacked HTTP-server instrumentation) inherits its parent's entry and is exported as INTERNAL, with a once-per-scope warning.

**Confirmed reused-span association:** an outer user-owned SERVER span eligible for reuse under section 8 is associated with the monitored request at middleware entry, after activation and framework enrichment and before downstream handling. This establishes its request-root entry even if Apitally missed `OnStart`. Any earlier processor entry is reconciled with this association; request initialization, exclusions and request sampling run once. Descendant spans started downstream inherit this request state. Spans and logs already completed before middleware entry are not recovered; unrelated unknown spans are still dropped.

**Confirmed middleware-created request roots:** when the incoming span is not eligible for reuse, Apitally creates a SERVER span with its selected provider and explicitly classifies it as the monitored request root. It retains the incoming parent context, preserving trace ID and parent relationships. A foreign local parent does not trigger the duplicate-SERVER conversion or unknown-parent drop rule for this designated request root. Foreign spans remain outside Apitally's pipeline; their providers and exports are unchanged.

**Confirmed request data source:** exclusion matching and SERVER span enrichment read the framework's request data (method, path, `User-Agent`, route), not span attributes, as in .NET. No old-semconv normalization is needed at request initialization, including on reused user-owned SERVER spans. Query redaction at export still covers stable and legacy query-bearing attributes on all spans, including those from user instrumentation.

**Confirmed websockets:** requests carrying `Upgrade: websocket` are excluded from traces, metrics and error capture. Go websocket libraries hijack the connection, and no Go instrumentation in scope emits per-message spans, so no per-message span filter exists.

**Fact:** Go has no implicit context. Descendant spans and logs are linked to a request only when application code passes the request context (`r.Context()`, or the framework equivalent) to tracers and loggers. This is standard Go OTel practice and is documented, not worked around.

## 6. Sampling and per-request buffering

**Inherited, within Go's observation boundary:** two-stage sampling with the deterministic trace-ID ratio test; callbacks fail open with a warning; per-request buffers of 1,000 spans and 1,000 log records; release once both transport observation and SERVER span end have completed; release order descendants, SERVER span, logs.

**Confirmed completion coordination:** for ordinary responses, transport observation completes when the SDK's observed handler chain returns or unwinds, after any framework error dispatch performed by Apitally. Fiber streamed responses instead complete observation when fasthttp finishes or aborts stream consumption (section 7), matching the reference SDKs' server-side streaming lifecycle. The snapshot contains the status, sizes, route and error state available at the supported boundary. Recovery outside the SDK and Gin writes after middleware-chain completion can happen later; their final response data is not promised (section 8). When the middleware created the SERVER span, it ends the span after observation completes. When it reused a user-owned SERVER span, the user's span may end before or after observation; release waits for both. Final observed transport attributes are attached to Apitally's export copy before response sampling and release. Waiting for a later span end does not extend observation beyond the supported boundary or recover missing response data.

**Confirmed late telemetry:** spans and logs that arrive after their request is released are dropped locally, as in .NET. In Go these come from goroutines that outlive the request while holding its context. Fiber stream-time telemetry remains eligible while the request awaits stream completion and SERVER span end; handler return alone does not release it. No completed-request cache.

**Confirmed callback span type:** all four span callbacks receive OTel Go's native `sdktrace.ReadOnlySpan`, as Python and JavaScript pass their native `ReadableSpan`. `SampleOnRequest` receives the live SERVER span at request initialization: at span start for Apitally-created spans, or at middleware entry for eligible reused spans. `SampleOnResponse` and the body-mask callbacks receive Apitally's export copy: a struct embedding the original `ReadOnlySpan` (embedding satisfies the interface's unexported method, the technique OTel's own `tracetest` snapshots use) that overrides `Attributes()` and `Resource()`, and `SpanKind()` for the duplicate SERVER rule in section 5, and carries the private payload stash (raw captured bodies) that the exporter reads back by type assertion. User exporters never see the copy. The embedded original is the ended span snapshot that the user's processors also receive: the copy owns its attribute storage, never mutates slices returned by the original, and callbacks treat the span as read-only. For Apitally-created SERVER spans, `SampleOnRequest` runs in `OnStart`; the middleware prepares request state before starting the span and passes available request attributes as span start options. For reused SERVER spans, it runs once at middleware entry after framework enrichment, rather than in the earlier `OnStart`. It sees attributes available at that point; route templates resolved later by the framework are not yet available. Exclusions precede sampling in both paths.

**Confirmed sampling callbacks:** `SampleOnRequest` and `SampleOnResponse` are `func(span sdktrace.ReadOnlySpan) (rate float64, ok bool)`. `ok == false` abstains: the request stage falls back to `SampleRate`, the response stage keeps the earlier decision. A rate outside [0, 1], NaN or a panic warns and keeps (fail open). Keep and drop are `1, true` and `0, true`; the comma-ok result is Go's idiom for an optional value.

## 7. Capture pipeline

**Confirmed body-mask callbacks:** `MaskRequestBody` and `MaskResponseBody` are `func(span sdktrace.ReadOnlySpan, body []byte) []byte`, span first as in Python and .NET. They receive the decompressed body and return the replacement; a nil or empty result or a panic yields `[REDACTED]`, and an over-cap result yields `[BODY_TOO_LARGE]`. They run on the span batch processor's export goroutine against the export copy.

**Inherited:** header-only capture decisions, content-type allowlist, 50,000-byte cap with `[BODY_TOO_LARGE]`, complete bodies only, payloads invisible to user exporters, redaction before export, fail closed. Processing order: bounded decompression, mask callback, JSON parse, field redaction, compact serialization. Body capture is limited to the transport observation boundary in section 8; a response body not known to be complete is omitted rather than exported as a complete payload.

**Confirmed decompression:** `gzip` and zlib-wrapped `deflate` via the standard library. The standard library has no Brotli decoder, so `br` bodies are skipped (attribute absent), as the spec allows.

**Confirmed processing location:** decompression, masking and redaction run on the span batch processor's export goroutine, never on request-serving goroutines.

**Confirmed transport mechanics:**

- **net/http frameworks (Chi, Echo, Gin):** wrap `Request.Body` and the `http.ResponseWriter` with bounded capture and byte counting. Preserve relevant directly asserted interfaces, including supported `http.Flusher`, `http.Hijacker` and `http.Pusher` capabilities; `Unwrap` additionally supports `http.ResponseController` and is not a substitute for direct interface forwarding. Review and reuse v0's forwarding logic where applicable. Gin uses a dedicated wrapper satisfying its full `gin.ResponseWriter` interface, including `WriteString`, status and header-commit methods. Supported alternate write paths participate in capture and count successfully written bytes exactly once. Wrapping preserves SSE flushing and connection-upgrade behavior without user changes. The request body wrapper captures only bytes the application reads and never reads more itself; a captured request body is complete when EOF is observed. Request size comes from `Content-Length`, else from the byte count at EOF; a body the application never reads to EOF has no determinable size without `Content-Length`.
- **Fiber (fasthttp):** request and ordinary response bodies are already buffered in memory by fasthttp, so capture copies at most 50,000 bytes after the handler. Request bodies with Fiber's `StreamRequestBody` enabled are not captured. Response streams, including `SendStream` and `SetBodyStreamWriter`, use bounded capture during consumption as described below. Request metadata, headers and other retained values are copied before middleware returns; later completion never reads a reused Fiber context.

**Confirmed Fiber response-stream observation:** wrap the final response stream, forwarding reads without draining it or buffering the whole stream. Count bytes consumed by fasthttp and observe its error-aware stream-close lifecycle to finalize once on completion or abort. EOF alone is not the completion signal: fixed-length and skipped-body responses can close without an EOF read. Preserve the original stream's close behavior and error propagation, including the distinction between `Close` and `CloseWithError`. Duration includes the server-side streaming lifecycle, not proof of client consumption. Response size uses the declared size when available, otherwise the counted size on complete consumption; an unknown final size after an abort is omitted. When response-body capture is enabled and headers make the body eligible, keep owned copies of observed chunks up to the shared cap. Crossing the cap discards the buffer and yields `[BODY_TOO_LARGE]`; an incomplete partial buffer from an aborted stream is omitted. Complete captured bodies follow the same decompression, masking and redaction pipeline as buffered responses. The middleware-created SERVER span ends at this completion boundary, and request release remains coordinated with SERVER span end under section 6. This internal integration requires no extra setup.

## 8. Transport observation, routes, frameworks

**Inherited route and client data:** `http.route` is the parameterized template including group and mount prefixes; unmatched requests carry no route. Client address follows the framework's own resolution (`c.ClientIP()`, `c.RealIP()`, `c.IP()`, `r.RemoteAddr`), never SDK-parsed forwarding headers.

**Confirmed monitored scope and observation boundary:** `Init(app, cfg)` instruments the application's handler chain and preserves its serving API. The SDK owns installation of its internal components; recovery remains application-owned. Ordinary responses and error-handler responses dispatched by Apitally are observed; Fiber streamed responses retain observation through stream completion or abort (section 7). Other responses produced outside the supported boundary, including later writes by outer recovery, are not guaranteed to be observed. This is an explicit Go adaptation of the shared whole-application transport guarantee.

**Confirmed registration for Chi, Echo and Fiber:** initialization registers Apitally middleware through the framework's normal registration API at the call site. Call `Init` before ordinary application middleware, groups and routes so their request helpers have state. Recovery registered before initialization runs outside Apitally; recovery registered afterward runs inside it. Existing recovery retains its registration and behavior. Simple setup does not guarantee both original-panic capture and a complete recovery response; the ordering-dependent limits below are part of the supported contract.

**Confirmed Gin setup and observation boundary:** `Init` retains `gin.Default()` and ordinary serving through `r.Run()`; it returns no HTTP handler and does not configure an HTTP server. Using Gin's exported root handler chain, initialization places the transport observer before existing middleware and adds an internal panic-capture/repanic handler after existing recovery. Register recovery before `Init`, then ordinary middleware, groups and routes afterward. The observer prepares request state before the existing middleware runs and finalizes after recovery returns, observing its actual response status, headers, body and size. No separate public panic-capture middleware is required.

**Accepted Gin adaptation:** responses that bypass Gin middleware, including automatic redirects, are not observed. Transport writes performed by Gin after the middleware chain returns are not fully observed, including default 404/405 bodies and their final headers and sizes. Gin finalization occurs at middleware-chain completion, not after the engine's final write. Ordinary routed responses and supported recovery responses remain observed. This explicitly narrows the shared whole-application transport guarantee; unmatched requests remain excluded from request metrics under the inherited eligibility rules.

**Confirmed SERVER span creation:** the Apitally middleware creates the SERVER span on all six integrations, as the shared design permits; no stock framework instrumentation is used. Stock Go instrumentation is uneven: `otelgin` and Fiber v3's `gofiber/contrib/v3/otel` are adequate, but contrib `otelecho` was removed in October 2026 in favor of the non-semver `labstack/echo-otel`, `otelfiber/v2` mixes old and stable semconv, `otelchi` uses old semconv, and `otelhttp` reads `http.route` only at span start, omits `url.query`, and derives `client.address` from `X-Forwarded-For` itself. The middleware:

- extracts upstream context with the global propagator, falling back to W3C TraceContext;
- sets stable HTTP semconv attributes from the framework's own request data (method, route template, scheme, host, path, query, client IP, status, sizes);
- names the span `{method} {route}`, or `{method}` without a route;
- puts the span into the request context (`r.Context()`, Fiber's user context) so handler spans nest under it;
- uses the framework module path (e.g. `github.com/apitally/apitally-go/gin-v1`) as instrumentation scope.

When the incoming request context already holds a recording SERVER span, because the application installed OTel HTTP instrumentation outside Apitally's middleware, reuse requires an attached global SDK provider and `span.TracerProvider()` matching that provider. This ensures Apitally's processor observes the span's end. The middleware associates an eligible span with enriched request state at entry (sections 5-6) and creates none. A recording span in context alone is not sufficient for reuse. Otherwise the middleware creates a SERVER span with its selected provider, retains the incoming parent context and classifies its span as the monitored request root (section 5). In private-provider mode this creation is unconditional, even when a foreign provider produced a recording SERVER span. Instrumentation installed inside Apitally's middleware produces a SERVER span with a local parent, handled by the duplicate rule in section 5.

### Server errors

**Inherited, with the panic-status adaptation below:** request-local error state keeps the first captured error; a server error counts only with a captured error and recorded status 500; captured errors become the SERVER span's `exception` event (first only); cancellation is never captured. Recorded status is the observed response status, except for the explicit panic-unwind assumption below. A 500 response alone does not imply a captured server error.

**Confirmed automatic capture sources:**

- **Gin panics under pre-existing recovery:** an internal handler inside the existing recovery captures the original panic value and panic-site stack, then re-panics unchanged. It neither writes a response nor finalizes the request. The outer observer finalizes after recovery completes, using its actual response status, headers, body and size, including custom recovery responses. Both capture locations use the first-error guard, so a panic is recorded at most once. Recovery added later inside the capture handler can consume the panic before capture, with the same limitation as other inner recovery.
- **Panics reaching Apitally's observer:** its deferred function captures the original panic and panic-site stack, finalizes the observed request state, then re-panics unchanged. This preserves existing recovery or net/http connection-abort behavior. The recorded status is the committed status if the response has started, otherwise an assumed 500. That assumption is telemetry only; it does not mean a 500 response was sent. Later outer recovery may write another status, headers or body, which Apitally does not observe.
- **Panics consumed by recovery inside Apitally:** the observer sees the response or returned error from recovery, but not the original panic or its panic-site stack. A recovery-returned error is captured through the ordinary Echo/Fiber error channel; it may be converted and have no original stack. Without another captured error, even an observed 500 does not increment captured-server-error counts. Chi's default Recoverer supplies no error channel.
- **Echo and Fiber:** errors returned by the handler chain. The middleware invokes the framework's error handler itself (`c.Error(err)` in Echo v4, `c.Echo().HTTPErrorHandler(c, err)` in Echo v5, `c.App().ErrorHandler(c, err)` in Fiber, with Fiber's `SendStatus(500)` fallback when the handler fails), observes the final status, and returns nil, so the error handler runs exactly once. Returning the original error would make the framework invoke its error handler a second time, writing the body twice with unguarded custom handlers; Fiber's logger middleware follows the same precedent. Consequence: middleware registered outside Apitally's does not see the returned error. v0 read the status before the framework's error handler ran.
- **Gin:** the first entry in `c.Errors` (`c.Error`, `c.AbortWithError`).
- **Chi:** panics and explicit `CaptureError` only; net/http has no error channel.

The recorded-500 rule filters all sources, so for example `echo.NewHTTPError(400)` contributes nothing. Unmapped validation errors returned to Echo or Fiber produce the frameworks' default 500 and therefore count as server errors, not validation errors. Errors matching `errors.Is(err, context.Canceled)` and the `http.ErrAbortHandler` panic are not captured.

**Accepted recovery-response limits:** when a panic unwinds Apitally before outer recovery writes its response, captured headers and size information describe only what was available at unwind, not the final recovery response. The recovery payload is unavailable; incomplete bodies are omitted. Custom recovery statuses can differ from the committed-or-assumed status Apitally recorded. Request duration ends at Apitally's observation boundary and excludes later outer recovery work. These limits apply to metrics, response sampling and request-detail capture; they are not just a custom-status approximation.

**Confirmed exception fields:**

- `type`: the Go type name of the captured value with one pointer level removed (`fs.PathError`, `pq.Error`), as in v0; `string` for `panic("boom")`.
- `message`: `err.Error()`, or `fmt.Sprint(v)` for non-error panic values.
- `stacktrace`: built from `runtime.Callers` and formatted deterministically as `function\n\tfile:line` per frame, without goroutine IDs, arguments or `+0x` offsets, so the displayed value is also a stable aggregation key. Panics use the stack taken inside the deferred recover (which includes the panicking frames), excluding Apitally's own frames. Explicit `CaptureError` uses the stack at its call site. Automatically captured returned errors and `c.Errors` entries have an empty stacktrace: Go errors carry no stack, and the middleware's own stack is meaningless.

### Validation errors

**Confirmed explicit deviation from the shared spec:** Go frameworks do not own validation responses; handlers typically validate and write their own 400 (Gin's `c.ShouldBind*`, Chi, Fiber v2), which no SDK mechanism can observe. Validation capture is therefore automatic where the framework sees the error, plus a public `CaptureValidationError(c/ctx, err)` helper, kept from v0, alongside the other request helpers in section 13.

- **Automatic:** errors in the framework error channels from the server error section (Gin `c.Errors` from `c.Bind*`, errors returned to Echo and Fiber, including Echo's `c.Validate` and Fiber v3's `c.Bind()` with a struct validator) when the final status is 400 or 422. Fiber v3's `c.Bind().WithAutoHandling()` converts validator errors into a plain 400 `*fiber.Error` without the original error, so its details are unavailable; applications using it call `CaptureValidationError` or map errors themselves.
- **Explicit:** `CaptureValidationError` records the error's details in request-local state; they are committed at transport completion under the shared eligibility rules (routed, not `OPTIONS`), regardless of status.

**Confirmed recognition:** `go-playground/validator/v10` errors, the de facto Go validator (used by Gin's binding), recognized by method set rather than by import: a slice in the error's `Unwrap` chain whose elements have `Namespace()`, `Field()`, `Tag()` and `Error()`. This avoids adding the validator's dependency tree to modules whose users do not use it. Other errors are ignored. Each element maps to:

- `field`: `Namespace()` without the leading struct name (`Address.City`, or `address.city` with a registered JSON tag-name function), preserving the validator's own path string;
- `type`: `Tag()` (`required`, `email`);
- `message`: `Error()`;
- `source`: empty, because the bind source is unknown to the SDK.

## 9. Logs

**Inherited:** request-scoped application logs only, linked via `apitally.request.server_span_id`; records without a request are dropped; SDK and OTel diagnostics are never captured; `CaptureLogs=false` disables only application-log capture; bodies truncated to 2,048 characters after masking.

**Confirmed startup event:** `paths` is the union of the routes of all apps passed to `Init` before activation, enumerated with the framework's route listing (`gin.Engine.Routes`, `echo.Echo.Routes`, `fiber.App.GetRoutes`, `chi.Walk`), omitting `HEAD`, `OPTIONS` and catch-all method registrations. `framework` is `chi`, `echo`, `fiber` or `gin`. `versions` contains `go` (`runtime.Version()`), the framework module version from `debug.ReadBuildInfo()`, and `app` when configured. `openapi` is omitted: none of the supported frameworks produces an OpenAPI document natively.

**Inherited:** error aggregation (100 + 100 distinct errors, per-consumer counts, drained before log flushes) and consumer-update events (10,000-entry LRU of normalized payload hashes).

**Confirmed capture surface:** `log/slog` only; zap, zerolog and logrus are out of v1 scope.

- **Automatic, for independent application default handlers only:** at activation, inspect `slog.Default().Handler()` and leave the standard library's `defaultHandler`, including handlers derived through `With` and `WithGroup`, unchanged. Recognition uses its pointer kind, full package path `log/slog` and concrete type name `defaultHandler`, with coverage on every supported Go version; handler identity is not an ownership test. For independent application handlers, wrap with Apitally's capture handler, install with `slog.SetDefault`, then restore the `log` package's previous writer and flags. Restoring preserves steady-state `log` output, including source locations that `slog.SetDefault` would otherwise drop; the global transition is not atomic. `log` records then bypass capture, which loses nothing because they carry no request context. Skipped when the default handler is already an Apitally handler, including a derived Apitally handler.
- **Standard library default handler left unchanged:** wrapping it can permanently deadlock the application. `slog.SetDefault` redirects the `log` package into the wrapper while the original handler writes through the `log` package; a concurrent `log` or `slog` call between that redirect and the writer restoration blocks on the `log` package's output mutex, which the restoration also needs. No public API makes the switch atomic. Replacing it with a `TextHandler` (v0) would change the application's output format and its `log` flag, prefix and `SetLogLoggerLevel` behavior. Applications on the standard library default handler get automatic capture by installing an independent handler, or explicit capture through a separate logger as described below; documentation states this, and a debug message records it.
- **Explicit:** `apitally.NewSlogHandler(next slog.Handler) slog.Handler` (root package, re-exported) for loggers not derived from `slog.Default()` at activation, such as loggers built with `slog.New(h)` and injected, or a `slog.Default()` stored before activation. A wrapper around the standard library default handler, including a derived handler, may be used by a separate `slog.New(...)` logger that leaves globals unchanged. Installing an Apitally wrapper globally with `slog.SetDefault` requires an independent underlying handler, such as `slog.NewTextHandler` or `slog.NewJSONHandler`; wrapping the standard handler globally creates a recursive logging path and is unsupported. Documentation examples follow this distinction.
- **Previously bound attributes:** attributes bound into the wrapped handler before wrapping (`h.WithAttrs(...)` before `slog.SetDefault`) remain in the application's output but are not visible to the capture handler, because handlers expose no public accessor for them. Attributes added through `With`/`WithGroup` after wrapping are captured. Documented; installing `NewSlogHandler` innermost captures all of them.

Records are linked to a request only when the request context is passed (`slog.InfoContext(ctx, ...)`, `logger.Log(ctx, ...)`). Records without one, including all `log` package output, are dropped. `slog.SetDefault` calls after activation replace the automatic wrapper; first-request activation makes this unlikely. The capture handler forwards every record to the wrapped handler unchanged and reports `Enabled` as the wrapped handler does, so user-configured levels apply.

**Confirmed record content:** the body is the slog record's `Message`. All attributes, including those added through `Logger.With` and `WithGroup`, are exported as OTel log record attributes, with groups as nested map values and slog value kinds mapped to OTLP values following the `otelslog` bridge's conversion. The capture handler applies that conversion when it builds the captured record, before `MaskLogRecord`, as .NET does: `LogValuer`s are resolved and every `KindAny` value is replaced with an Apitally-owned value (maps become nested group values, slices and arrays become newly allocated `[]any` of converted items, byte slices are copied, and structs, errors and other types become strings). Native slog kinds are unchanged. The captured record therefore holds no references to application objects. Values a callback sets are converted with the same rules at export. String attribute values are truncated to 2,048 characters, like the body. The instrumentation scope name is `slog` (spec section 8); `code.file.path`, `code.line.number` and `code.function.name` come from the record's `PC`. The server currently stores only message, level, logger and code location; exporting attributes now means they become visible as soon as the server stores them, without an SDK change.

**Confirmed record pipeline (adaptation, as in .NET):** no OTel log SDK or logger provider. The capture handler builds an internal record, buffered per request under section 6; released records go to a log batching goroutine with a ~1s schedule that encodes and appends them to the logs spool. SDK internal events (startup, error aggregates, consumer updates) are built directly as internal records with scope `apitally`, their event name and no trace context, and take the same path, bypassing masking and truncation. OTel Go's log SDK is pre-1.0 (`sdk/log` v0.22.0) at the Go 1.25 floor, a module path that applications may resolve to an incompatible newer version; only its batch processor would be used, since the exporters' protobuf conversion is internal and its records can only be created through an OTel `Logger`.

**Confirmed `MaskLogRecord`:** `func(record slog.Record) (slog.Record, bool)`, using Go's own log record type. It runs synchronously in the capture handler, before request buffering, on a fresh record that already includes the logger's `With`/`WithGroup` attributes and the converted, Apitally-owned values described above, so it sees exactly what will be exported and cannot affect the application's own log output, even when it modifies a value in place. Callbacks therefore cannot type-assert `slog.Any` values back to application types. Returning `(r, true)` exports the returned record; `false` or a panic drops it. Because `slog.Record` is a value type, returning a modified copy is the ordinary way to modify it, so the shared rule that a replacement record drops does not apply.

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

**Confirmed primary entry point:** `Init(app, cfg)` in each framework package, with no return value, `cfg *Config`, and the framework's typed app argument (`*gin.Engine`, `*echo.Echo` for each major, `*fiber.App` for each major, `chi.Router`). The name follows the reference Python SDK's `apitally.init`, with Go capitalization. Initialization configures Apitally and owns internal component registration, route enumeration and applicable lifecycle hooks. Repeated setup of the same app does not duplicate instrumentation; process-wide configuration ownership remains as described in section 3. Users initialize before ordinary application middleware, groups and routes, subject to Gin's existing-recovery ordering in section 8. Configuration is described in section 3, shutdown in section 4.

Initialization preserves the application's ordinary serving API and application-owned recovery. The two-argument call is the complete setup surface: recovery arguments, additional capture middleware and a returned HTTP handler are not required. The observation and panic-capture limitations in section 8 are supported behavior, not unmet implementation requirements.

```go
r := gin.Default()
apitally.Init(r, cfg)
r.Run(":8080")
```

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

`Consumer` is `struct { Identifier, Name, Group string; Attributes map[string]string }`. An empty attribute value deletes the key (spec section 9.3). Because Go map iteration order is random, the spec's "first 10 valid entries" are taken in lexicographic key order, sorted before validation and the limit; when one request calls `SetConsumer` more than once, earlier calls' keys come first. `SetRequestAttributes` takes OTel's native `attribute.KeyValue`, variadic like `span.SetAttributes`; it is the Go form of `set_request_attribute`. `CaptureError` uses Go's error vocabulary for `capture_exception`.

Initialization installs the request-state holder at Apitally's observation boundary. Ordinary application middleware registered after initialization can use request helpers. Middleware outside that boundary does not receive a guarantee of request-state availability. Framework-specific documentation explains the supported registration order and recovery-dependent capture limits in section 8.

### Manual tracing

**Confirmed:** `StartSpan(ctx, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span)` in the root package, re-exported by the framework packages. It creates an INTERNAL span under tracer scope `apitally.otel` from the global tracer provider and adds the caller's `code.function.name`, `code.file.path` and `code.line.number` (`runtime.Caller`). It returns native OTel types; `defer span.End()` is Go's block form. There is no function wrapper: Go has no decorators, and closure-based wrappers are unusual Go. Outside a monitored request the span is not recorded by Apitally's fallback sampler.

```go
ctx, span := apitally.StartSpan(ctx, "search_books")
defer span.End()
```

No contrib setup wrappers: Go instrumentations are already one-liners (`otelhttp.NewTransport`, `otelsql.Open`, `redisotel.InstrumentTracing`); documentation shows them.

### Migration contract

The migration guide covers: middleware registration replaced by the framework package's `Init` entry point; `ClientID` replaced by `WriteToken` (and `APITALLY_WRITE_TOKEN`); the nested `RequestLoggingConfig` flattened into `Config` with the shared `Capture*` names; request logging and log capture on by default; `ExcludeCallback` replaced by the keep-probability sampling callbacks; `SetConsumerIdentifier` and the `ApitallyMiddleware`/`ApitallyConfig`/`ApitallyConsumer` aliases removed; `Consumer.Attributes` added; the `gin` module renamed to `gin-v1`; the new `Shutdown` call; `slog.InfoContext` with the request context required for log linkage; the Go 1.25 floor.

## 14. Sentry integration

**Confirmed scope deviation:** deferred beyond v1, as in .NET; v0 Go had none. Ordinary error capture and aggregation are unaffected. Go cannot detect an optional dependency without importing it, so automatic detection would put `sentry-go` into every user's build. The intended future path is a separate opt-in module (`github.com/apitally/apitally-go/sentry`) registering a `sentry-go` event processor, added without changing existing APIs.

## 15. Cross-language posture and explicit adaptations

Wire attributes, scope names, default redaction and exclusion patterns, the sampling convention, body privacy rules, error identities and limits, buffer caps, spool rules and delivery behavior remain shared requirements. Go adaptations:

| Shared design area | Go treatment |
| --- | --- |
| Setup entry point | Typed per-framework `Init(app, cfg)`, named after Python's `apitally.init`, with no return value and a flat `Config` from `NewConfig()`. The SDK owns internal component registration and per-app duplicate setup; application serving and recovery stay application-owned. No unified detecting entry point, since each framework is a separate module. |
| Runtime ownership | Process-global, first configuration wins, as in Python and JavaScript. |
| SERVER spans | Created by Apitally's middleware on all frameworks; no stock framework instrumentation. User-owned outer SERVER spans are reused only when their provider matches the SDK provider Apitally attached to; request association, exclusions and request sampling run at middleware entry for reused spans. |
| Tracer provider | Attach to a global SDK provider; if unset (identity with the value captured at package initialization), register Apitally's provider and, if also unset, the W3C propagator as globals; private SERVER-only provider for foreign providers, including noop. Private mode always creates its own monitored SERVER span, retaining an incoming parent without applying duplicate-SERVER or unknown-parent rules to this request root. No provider option. |
| Activation | First request, plus Fiber `OnListen` outside prefork masters; `testing.Testing()` suppresses activation. |
| Shutdown | Explicit `Shutdown(ctx)` is the only terminal path; Fiber shutdown hooks run a non-terminal flush with a fixed 5-second deadline. No exit hook or signal handling. |
| Request helpers | Explicit framework context or `context.Context` arguments instead of implicit request context. |
| Validation capture | Automatic recognition of `go-playground/validator` errors in framework error channels plus a public `CaptureValidationError`; explicit deviation from the spec's no-API rule. |
| Exceptions | Go errors and panics; Go type name without pointer; deterministic `runtime.Callers` stacktraces; empty stacktrace for automatically captured returned errors. Original panic capture depends on recovery ordering. Gin captures inside pre-existing recovery and observes outside it; other inner recovery can hide the original panic. Echo/Fiber returned errors are dispatched by Apitally's middleware, which returns nil. |
| Sampling callback result | Comma-ok `(rate float64, ok bool)`. |
| Span callback type | Native `sdktrace.ReadOnlySpan`; export copy embeds the original span. |
| Logs | `log/slog` only; independent application default handlers wrapped automatically; original and derived standard library default handlers recognized by concrete type and left unchanged. `NewSlogHandler` supports separate loggers; global installation requires an independent underlying handler. Linkage requires the request context; attributes exported as log attributes. |
| Log pipeline and callback | Apitally-owned records without OTel log SDK, as in .NET; `MaskLogRecord` uses `slog.Record` with value semantics, holding values converted to Apitally-owned representations before the callback, as in .NET. |
| Pattern options | `[]string`, case-insensitive by default, inline flags respected, as in Python and .NET. |
| Transport and completion | Ordinary observation ends at Apitally's handler-chain completion or unwind; Fiber streams retain observation and duration measurement through fasthttp stream completion or abort, with copied request state and coordinated span/request release. Eligible streamed response payloads follow the same bounded capture and privacy rules as buffered responses. Outer recovery responses may have missing headers, bodies and final sizes, an assumed status and duration excluding later recovery work. Gin preserves `gin.Default()` and `r.Run()`; responses bypassing middleware (including automatic redirects) and final engine writes (including default 404/405 payloads) are not fully observed. |
| Brotli | Not decoded (no standard library decoder); `br` bodies are not captured. |
| Process gauges | `gopsutil/v4`. |
| Manual tracing | `StartSpan(ctx, name)` returning native OTel types; no function wrapper, no contrib wrappers. |
| OpenAPI | Omitted from the startup event. |
| Sentry | Deferred beyond v1. |
| Late telemetry | Dropped after release, as in .NET. |
| Fork and context isolation | Not applicable. |

## 16. Code style and testing

**Confirmed:** standard `go test` with `-race`, `testify` as in v0, `httptest` servers driving small real apps per framework. Assertions run against decoded OTLP payloads captured by a local stub endpoint or an in-process spool reader. No test doubles for Apitally's own code. Integration tests follow the Python suite's scenario names where they apply, and share names across the Go framework modules. Recovery tests verify unchanged application responses and the documented ordering-dependent capture behavior, not universal original-panic or complete recovery-response capture. Shared test expectations apply within the explicit Go observation adaptations in sections 6-8. Response-writer tests compare SSE flushing and supported upgrades with their unwrapped behavior and verify once-only size counting and capture through supported alternate write paths, including Gin's `WriteString`. Fiber stream tests cover fixed-length and unknown-length completion, skipped bodies and aborted streams, asserting original close behavior, duration through completion, eligible stream-time telemetry and once-only request release. Streamed response-body capture tests use the same content-type, cap, completeness, masking and redaction expectations as buffered responses. Integration tests run at each framework module's floor and latest release (section 1).

Add a Go language adapter and Go bookstore apps to the sibling [SDK test harness](../../sdk-tests/README.md), which stops apps with SIGTERM: the Go apps use the standard `signal.NotifyContext` graceful-shutdown pattern with `Shutdown`.

## 17. POCs

Research code in `pocs/`, each a separate module with a README recording versions, evidence and conclusions. All ran with `-race` on Go 1.27.1 and Go 1.25.14 (slog also on 1.26.8), and on OTel Go v1.46.0 and v1.47.0 where OTel is involved.

1. [Export span copy](../pocs/export-span-copy/README.md): **confirmed.** The embedding copy satisfies `ReadOnlySpan`, passes through the stock `BatchSpanProcessor` with its concrete type and stash intact, and leaves user exports unchanged. Findings recorded in sections 2 and 6.
2. [slog default wrapping](../pocs/slog-default-wrapping/README.md): **disproved for original and derived standard library default handlers** (permanent deadlock in the activation window); confirmed for independent application handlers. Concrete-type recognition covers derived standard handlers on all supported Go versions. Explicit standard-handler wrapping is safe only on a separate logger without global installation. Section 9 changed accordingly.
3. [Provider detection](../pocs/provider-detection/README.md): **probe span disproved** (exported by recording foreign providers; misclassifies noop and request contexts). Replaced by identity comparison with values captured at package initialization (section 2). Delegation of cached tracers and live `RegisterSpanProcessor` confirmed.
4. [Framework error handlers](../pocs/framework-error-handlers/README.md): confirms Echo and Fiber error dispatch must return nil and Fiber v3 auto-handling binds lose validation details. Validation recognition without importing the validator, deterministic stacks and request context storage confirmed. Panic-order observations establish the accepted Go observation limits in R2-R3: Gin uses split capture around pre-existing recovery; the other frameworks retain simple registration and ordering-dependent panic/response capture. The POCs demonstrate those limits, not universal complete recovery telemetry. Section 8 records the supported contract.

