# Apitally Go v1 design review: feasibility, simplicity and Go idiom

Date: 2026-10-07
Reviewed revision: `6408616` (`docs/design.md`)
Status: Open; 15 findings for discussion.

This review looks for decisions, rules and guarantees in the Go design that cost disproportionate implementation complexity, where a slightly altered rule allows a much simpler implementation, and for choices that a senior Go developer would read as translated from another language. Findings and recommendations are input for discussion, not requirements. Resolving a finding means recording the user's decision here and applying the agreed documentation changes before moving to the next finding.

Findings that reopen an earlier decision (R1, R11 and R12 in [the first review](design-review.md), C3 in [the .NET consistency review](design-review-2.md)) are included only where the argument is new; each says what is new.

## Method

Four independent reviewers examined:

1. Go idiom and public API surface (sections 1, 3, 4, 6, 8, 9, 12, 13, 15).
2. Tracing core: provider integration, request model, sampling, buffering and release (sections 2, 5, 6, 8, 10).
3. Framework transport: response writers, Fiber streams, error dispatch, route enumeration (sections 4, 6-9, 13, 16).
4. Configuration, lifecycle, logs, export and metrics (sections 3, 4, 9-12).

Reviewers checked claims against dependency source in the module cache, the Python, JavaScript and .NET v1 SDKs, v0 on `main`, and throwaway Go programs (Go 1.25.8, OTel v1.46.0 and the framework versions pinned by the POCs and floors). The main reviewer independently re-verified every finding marked High and every factual claim the recommendations depend on (see the validation record). Weak, speculative or already-decided findings were dropped or listed under "Concerns not promoted".

Priorities:

- **High:** the specified mechanism breaks applications, cannot be built as written, or adds major machinery that a small rule change removes.
- **Medium:** a concrete idiom, ergonomics or complexity problem that should be settled before implementation planning.
- **Low:** a small rule clarification or implementation gotcha worth recording.

## Findings

### S1. The slim OTLP proto module panics at startup next to any official OTLP exporter

**Open | High | Design sections 1 and 10, lines 36, 229**

`go.opentelemetry.io/proto/slim/otlp` v1.11.0 registers the same protobuf file names (`opentelemetry/proto/common/v1/common.proto` and so on) as the canonical `go.opentelemetry.io/proto/otlp`. Every official OTel Go OTLP exporter (`otlptracehttp`, `otlptracegrpc`, `otlpmetrichttp`, `otlploghttp`) depends on the canonical module. Protobuf's default conflict policy is `panic`, so an application that links both panics in package initialization, before `main`. Those applications are exactly the existing-OTel users section 2 is designed around. Slim v1.11.1 moved to a separate `opentelemetry.proto.slim` namespace, but it requires Go 1.26 and breaks the Go 1.25 floor.

**Recommendation:** use the canonical module and import only its data packages (`common/v1`, `resource/v1`, `trace/v1`, `logs/v1`, `metrics/v1`). Marshal `TracesData`, `LogsData` and `MetricsData`; each has `resource_*` as field 1, so the bytes are identical to the corresponding `Export*ServiceRequest`. Never import the `collector/...` packages, which are what pull in gRPC. Canonical v1.11.0 declares `go 1.25.0`, so section 1's separate slim floor sentence goes away.

**Lost:** nothing. The data packages compile without gRPC and add no gRPC requirement to the user's `go.mod`.

**Evidence:** reproduced by the main reviewer: importing slim v1.11.0 `trace/v1` alongside `otlptracehttp` v1.46.0 panics with `proto: file "opentelemetry/proto/common/v1/common.proto" is already registered ... previously from: "go.opentelemetry.io/proto/otlp/common/v1"`. A canonical-only build has `go list -deps | grep -c grpc` = 0 and no gRPC line in `go.mod`. Module `go` directives: canonical v1.11.0 `go 1.25.0`, slim v1.11.1 `go 1.26.0`. No POC exercised the slim module.

### S2. The Fiber response-stream wrapper cannot be installed through fasthttp's public API

**Open | High | Design sections 1, 6, 7 and 16, lines 38, 116-118, 139**

Section 7 requires wrapping the final response stream, observing fasthttp's error-aware close, exposing the original stream's zero-copy path and counting consumed bytes. The only public way to replace a response stream is `Response.SetBodyStream`, which calls `ResetBody()` and therefore closes the current stream before the wrapper is installed (fasthttp v1.51.0 `http.go:247-251`, `640-651`; unchanged in v1.69.0). All other assignments to `bodyStream` are internal. Re-wrapping through the public API breaks real responses: with `SetBodyStreamWriter` (SSE) the client receives 0 bytes on fasthttp v1.50.0 and v1.73.0; with `SendFile`, v1.73.0 panics (`bug: fsFile.readersCount < 0`) and v1.50.0 reuses a pooled reader. The only working install is a reflect plus `unsafe` write to the unexported field.

The rest of the rule then mirrors more fasthttp internals:

- **Error-aware close:** `ReadCloserWithError` first appears in fasthttp v1.53.0, but Fiber v2.52.15 (latest) still requires v1.51.0. Every `fiber-v2` user would get a forced fasthttp upgrade, and the signal changes no outcome: declared-length accounting or EOF already decides completeness and size.
- **Zero-copy:** fasthttp selects its file path by concrete type (`*os.File`, `*io.LimitedReader` around one) or `io.WriterTo` on unexported readers. `bytes.Reader` and `strings.Reader` also implement `io.WriterTo`, so "file-backed" cannot be identified.
- **Content-Length:** a wrapper hides `*io.LimitedReader`, so `SendStream(io.LimitReader(f, n))` silently switches to chunked encoding.

That is five fasthttp behaviors to mirror and test at floor and latest on two Fiber majors.

**Recommendation (A, simplest):** finalize every Fiber request from an `io.Closer` stored with `RequestCtx.SetUserValue`. fasthttp calls `Close` on user values when it resets the request context, which happens after `writeResponse` and the final flush (v1.51.0 `server.go:2412-2467`) and also on write errors and aborts. Measured on Fiber v2.51.0, v2.52.15, v3.0.0 and v3.5.0: SSE closed after its last chunk, a slow `SendStream` after its last read, and a client abort after the failed write. This becomes the single completion path for all Fiber responses, so duration includes the write, like JavaScript `finish` and .NET `OnCompleted`. No stream wrapper, no zero-copy forwarding, no `CloseWithError`, no fasthttp floor bump.

**Lost under A:** `http.response.body.size` for streams without a declared length (SSE, `SetBodyStreamWriter`, `SendStream` with unknown size), and streamed response-body capture on Fiber for allowlisted content types (NDJSON or `text/plain` streams; SSE is not allowlisted). Both need an explicit Fiber adaptation in section 15. Decision 12 of [the first review](design-review.md) approved streamed payload capture; option A narrows it.

**Recommendation (B, if parity is required):** keep the user-value Closer as the once-only completion signal, and add exactly one reflect/`unsafe` swap of `bodyStream` that fails closed (no wrap) if the field is missing. Wrap only when the length is unknown or capture is eligible, and never wrap `io.WriterTo` or `*io.LimitedReader` streams. Use EOF or declared-length accounting instead of `CloseWithError`. This keeps size and capture for the cases that matter and drops zero-copy forwarding and the fasthttp floor bump, at the cost of one `unsafe` dependency on an unexported field.

Either option is a strict improvement on v0 and on `otelfiber`, which both end at handler return and drain non-SSE streams through `Body()`.

### S3. Always create the request span in the middleware; drop reuse of user SERVER spans

**Open | High | Reopens R11 and R12 | Design sections 2, 5, 6, 8 and 15, lines 102-104, 116, 120, 161, 309-310**

Apitally's middleware creates the SERVER span on all six frameworks. Reuse of an outer user-owned SERVER span is a second way to establish the request root, and almost all of the request-model complexity exists only for it:

- the eligibility test (recording, SERVER kind via `ReadOnlySpan` assertion, `span.TracerProvider()` matching the attached provider);
- association at middleware entry of a span whose `OnStart` Apitally may have missed, and reconciliation with an earlier processor entry (R11);
- `SampleOnRequest` running in `OnStart` for created spans but at middleware entry for reused spans;
- release coordination for transport completion and span end in either order, holding an ended SERVER snapshot. This happens in practice with `otelfiber` outside Apitally plus a Fiber stream;
- merging transport attributes into the export copy because the live span is not Apitally's;
- private-mode exceptions to the duplicate-SERVER and unknown-parent rules (R12).

It also leaves a 3-provider-mode by 4-incoming-span-state matrix with at least one unspecified cell: in owned mode an outer `otelhttp` span from the global (Apitally's) provider is a local-root SERVER span that inherited classification keeps as a request root. Whether that provider counts as "attached" is not stated. If it does not, the middleware also designates its own child, giving two roots in one trace.

R11 and R12 were decided on correctness. The new argument is that Go is the only SDK that owns the span. Python and JavaScript rely on stock instrumentation and .NET on the hosting activity, so they must classify at span start and coordinate either order (Python `span_processor.py` `defer_export`/`finish_export`, .NET `RequestState.TryClaimFinalization`). When the middleware owns the span it ends it after observation completes, and `End` runs `OnEnd` synchronously (`sdk/trace/span.go:457-463`), so release becomes a single event. The shared design allows this ("finish transport observation within the span's lifetime").

**Recommendation:**

1. The middleware always creates the request span with the selected provider, as a child of the incoming context. When the incoming parent is a valid local span (outer `otelhttp`, `otelgin`, `otelfiber`), create it with kind INTERNAL; Apitally's export copy reports it as SERVER through the existing `SpanKind()` override. The user's backend sees SERVER then INTERNAL, not a duplicate SERVER.
2. Only the middleware designates request roots. It runs exclusions from framework data before `Start`, inserts the root into the request map right after `Start` returns (no child can start before `next` runs), and runs `SampleOnRequest` there, in one place. The processor's `OnStart` only does parent lookup and the duplicate-SERVER flag; unknown local roots get no entry. In owned mode the fallback sampler recognizes the designated root through a context marker (samplers receive the `Start` context), so it records only Apitally's root and children of recorded local parents, not every SERVER span in the process (for example `otelgrpc` servers or excluded health checks).
3. Transport attributes are set on the live span before `End`; only payloads stay in the stash.

R11, R12, the eligibility rule, reconciliation, the `SampleOnRequest` split and two-order release disappear. Private-provider mode becomes only a different choice of provider.

**Lost:** for users with server instrumentation wrapped outside Apitally only: one extra INTERNAL span per request in their own backend (today's ineligible-reuse path already produces a full duplicate SERVER span, which is worse); Apitally's request span excludes time in outer middleware; attributes set by the outer span are not seen (section 5 already reads framework data instead). This deviates from the shared design's "reuse existing user-owned request spans rather than creating duplicates" in mechanism, not in outcome: no duplicate SERVER span reaches either backend.

### S4. `MaskLogRecord` on `slog.Record` makes the natural masking code leak

**Open | Medium-High | Design sections 9 and 15, lines 223, 319**

`slog.Record` has `Attrs` (iterates copies), `AddAttrs` and `Add` (append), and no way to replace or remove an attribute. Its documentation also says copies share state. User code written the obvious ways leaks the value:

- assigning `a.Value` inside `r.Attrs(...)` has no effect, so `password=secret` is exported;
- `r.AddAttrs(slog.String("password", "[REDACTED]"))` adds a duplicate, so both values are exported.

Correct masking means rebuilding the record with `slog.NewRecord` plus a recursive helper for groups, about 20 lines. Python and JavaScript mask an attribute in one line on a mutable OTel record. The SDK also has to build a `slog.Record` only for the callback, re-iterate it afterwards, and carve out the "replacement record is allowed" exception to the shared rule. The design's claim that "returning a modified copy is the ordinary way to modify it" does not hold for attributes. Masking is a privacy control, so silent leaks matter more than API nativeness.

**Recommendation:** an SDK-owned record type, as the shared design permits where the logging interface has no mutable record:

```go
type LogRecord struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   []slog.Attr
}

MaskLogRecord func(record *LogRecord) bool
```

Modify in place, return false to drop, matching the shared "modify or drop" contract with no replacement exception. The capture handler builds this struct directly from converted values, and the exporter reads it. .NET does the same with `LogRecordSnapshot`. The struct can carry code location fields if callbacks need them.

**Lost:** the native `slog.Record` type in the callback signature.

**Evidence:** Go 1.25.8 `log/slog/record.go` method set (`Clone`, `NumAttrs`, `Attrs`, `AddAttrs`, `Add`, `Source`), re-checked by the main reviewer; reviewer program demonstrating both leaks and slog's `!BUG` marker on shared copies.

### S5. Reuse the public `otlptrace` conversion instead of writing one

**Open | Medium | Design section 10, line 229; depends on S1**

Section 10 says "The OTel exporters' SDK-to-protobuf conversions are internal, so Apitally owns a conversion matching them." For traces this is wrong. `otlptrace.NewUnstarted(client)` returns an exporter whose `ExportSpans` runs the official conversion and hands `[]*tracepb.ResourceSpans` to the public `otlptrace.Client` interface (`Start`, `Stop`, `UploadTraces`). A five-line client that marshals `TracesData` and appends it to the spool reuses the official conversion exactly, which is what shared design section 10 asks for ("Reuse the official exporter's conversion ... where it is public"). That saves the ~440-line conversion and its parity tests. The export copy works unchanged because the exporter reads `Attributes()` and `Resource()` through the interface.

**Recommendation:** traces go through `otlptrace` with an Apitally `Client`; logs and metrics keep their own small encoders, since Apitally-owned records have no public conversion path.

**Lost:** nothing.

**Evidence:** the main reviewer built a minimal program with `otlptrace` v1.46.0 and a custom `Client`: it encoded an SDK span to `TracesData`, with no gRPC packages in `go list -deps`. `otlptrace` v1.46.0 declares `go 1.25.0`. The reviewer separately confirmed an overridden `Attributes()` on an embedding copy reaches the encoded output.

### S6. Private-provider mode must not put Apitally's span into the request context

**Open | Medium | Design sections 2, 5 and 8, lines 56, 104, 158**

In private-provider mode the global provider is a foreign implementation, for example a vendor OTel bridge that records and exports. Section 8 puts Apitally's span into the request context "so handler spans nest under it". The user's handler spans, created through the foreign global, then take Apitally's private span as their parent. The foreign backend never receives that span, so every existing trace gains a dangling parent. This contradicts section 5: "Foreign spans remain outside Apitally's pipeline; their providers and exports are unchanged."

**Recommendation:** in private-provider mode, keep Apitally's span only in Apitally's request state and leave the request context's span unchanged. Log linkage already goes through request state. One conditional in the middleware.

**Lost:** nothing material. Apitally receives only SERVER spans in this mode anyway. JavaScript sends no traces at all when it cannot attach; Python fails activation.

**Evidence:** reviewer program with a non-comparable foreign wrapper around an SDK provider: the handler span's parent was Apitally's private span ID, which was absent from the user's export.

### S7. Fiber: also store request state in `Locals`, so the natural context works

**Open | Medium | Design sections 8 and 13, lines 158, 267, 276**

Fiber v3's `Ctx` implements `context.Context`, and `Ctx.Value` reads fasthttp user values (Fiber v3.5.0 `ctx.go:661`), not the user context where section 13 stores request state. Fiber v2's `c.Context()` is `*fasthttp.RequestCtx`, whose `Value` also reads user values (fasthttp v1.51.0 `server.go:2753`). So `slog.InfoContext(c, ...)` on v3, `slog.InfoContext(c.Context(), ...)` on v2, and service code calling the root `apitally.SetConsumer(ctx, ...)` with either all compile and silently do nothing. [The .NET consistency review](design-review-2.md) lists the v2 case as a pitfall to document; it can be removed instead.

**Recommendation:** the Fiber v2/v3 middleware stores the request-state pointer under the same private key in both the user context and `Locals`. Where the context carries no span, the capture handler takes the span ID from request state. The state type must not implement `io.Closer`, because fasthttp closes closer user values on reset (S2 relies on that behavior deliberately, with a separate value).

**Lost:** nothing. `StartSpan(c, ...)` and raw `tracer.Start(c, ...)` still do not find the span, because OTel's context key is unexported; that remains a documentation item.

**Evidence:** reviewer Fiber v3.5.0 test: `c.Value` was nil without the `Locals` copy and returned the state with it. Main reviewer checked `Ctx.Value` (v3.5.0) and `RequestCtx.Value` (fasthttp v1.51.0-v1.53.0).

### S8. `Init` after routes fails silently on Gin and Fiber

**Open | Medium | Design sections 4 and 13, lines 92, 253**

Putting `apitally.Init(r, cfg)` just before `r.Run()` is the most likely Gin mistake, and on Gin and Fiber it leaves every existing route unmonitored without a diagnostic: Gin copies the handler chain into each route at registration (v1.12.0 `routergroup.go:241`), and Fiber applies `Use` only to later routes. Chi panics in this situation and Echo applies middleware to all routes regardless of order, so only Gin and Fiber fail silently. Shared design: "Unsupported setup forms report a clear error identifying the supported path." JavaScript already warns for this on Hono and Elysia (`apitally-js/src/hono/middleware.ts:50`, `elysia/middleware.ts:71`).

**Recommendation:** at `Init`, if `len(engine.Routes()) > 0` (Gin) or `len(app.GetRoutes(true)) > 0` (Fiber v2/v3), log an error naming the remedy, and keep instrumenting so later routes are still covered. `GetRoutes(filterUseOption ...bool)` exists at both Fiber floors.

**Lost:** nothing.

### S9. Automatic `slog.SetDefault` wrapping: consider explicit `NewSlogHandler` only

**Open | Medium | Reopens R1 and C3 | Design sections 9 and 15, lines 210-215, 318**

Two reviewers independently flagged that a library calling `slog.SetDefault` on the application's behalf would raise eyebrows with Go developers. The rule now needs reflect-based recognition of the standard library `defaultHandler` with a Go-version test matrix, detection of derived and existing Apitally handlers, a non-atomic `SetDefault` followed by restoring the `log` writer and flags, the C3 warning, and documentation for three classes of user. What is new since R1 and C3:

- **Coverage is narrow and its gaps are silent.** The idiomatic `logger := slog.New(h); slog.SetDefault(logger); svc := NewService(logger)` is never captured, nor is any `slog.Default()` stored before the first request, nor capture lost to a later `SetDefault`. None of these warn, so automatic capture gives false confidence exactly where it fails.
- **The default setup warns anyway.** An application with no slog configuration uses the standard library handler, so with the default `CaptureLogs=true` quickstart users see the C3 warning on their first request. Documentation has to teach `NewSlogHandler` regardless.
- **Go libraries do not do this.** `otelslog`, `sentry-go/slog` and New Relic's `nrslog` (`WrapHandler(app, handler)`, the same shape as `NewSlogHandler`) never call `SetDefault`. The other SDKs' automatic capture adds a handler alongside the user's; slog has one handler per logger, so automatic capture here means replacing application-owned global state. v0 did it only behind an opt-in.

**Recommendation:** explicit `NewSlogHandler` only. Optionally warn once at activation when `CaptureLogs` is true and no Apitally handler has been constructed (a counter), naming `NewSlogHandler`.

**Lost:** zero-change capture for applications that set an independent default handler and log through package-level `slog.*Context` functions; a recorded deviation from the shared preference for capture without changes to the user's logging setup. This is a product call that has been made twice; it is listed because the coverage and idiom evidence is new.

### S10. The root module's `internal` packages are an unversioned API between modules

**Open | Medium-Low | Design section 1, line 24**

Framework modules import the root module's `internal/...`, which in v1 holds the whole pipeline. Go's minimal version selection pairs a framework module with whatever root version the build selects, and v1 tells users to require the root module directly for service-code helpers (section 13), so Dependabot or Renovate will bump the two independently. A non-additive change to root `internal` then breaks the user's build inside Apitally code. Releasing all modules in lockstep (v0's `publish.yaml`) does not prevent this; it only controls what is published. OTel Go generates private copies of shared internals per module; aws-sdk-go-v2 imports root `internal` under an additive-only rule.

**Recommendation:** record a rule in section 1: within v1, root `internal` packages used by framework modules change additively only; a framework module raises its root requirement when it starts using new internal API; and CI builds the latest released framework modules against root HEAD before each release.

**Lost:** nothing; it constrains refactoring of internal APIs.

### S11. Forwarding `ReadFrom` drops body capture for every `io.Copy`, not just file sends

**Open | Medium-Low | Design section 7, line 136**

`io.Copy(w, r)` calls `w.ReadFrom(r)` whenever `r` lacks `WriteTo`. That covers upstream `http.Response` bodies, S3 objects and decompression readers. On Chi, an `application/json` proxy written with `io.Copy` silently loses capture: the wrapper's `Write` is never called. .NET's `SendFileAsync` is file-only; Go's `ReadFrom` is not, so "as .NET does for native file sends" is not what the rule achieves.

**Recommendation:** forward `ReadFrom` only when this response is not being captured (capture off, or content type not allowlisted at that point). Otherwise copy through the capturing `Write` via a writer-only shim.

**Lost:** `sendfile` only for allowlisted-type file sends with response capture enabled, which is off by default.

**Evidence:** reviewer program: `Write` called 0 times and `ReadFrom` once for both a JSON `io.Copy` and `http.ServeFile`; standard library `io.copyBuffer` order (`WriterTo` on source, then `ReaderFrom` on destination).

### S12. Inactive-middleware behavior is not specified

**Open | Low | Design sections 4 and 8, lines 86, 172**

Every user's `go test` run (`testing.Testing()`) and every local run without a token goes through the inactive path. If that path is a plain pass-through, Echo and Fiber return handler errors to outer middleware in tests and development but dispatch them and return nil in production. That control-flow difference is invisible until deployment.

**Recommendation:** state that framework-visible behavior (error dispatch, returning nil, writer wrapping) is identical whether or not Apitally is active; only telemetry is gated. One code path instead of two.

**Lost:** nothing.

### S13. `Init(app, nil)` and configuration copying are undefined

**Open | Low | Design sections 3 and 13, lines 72-80, 253**

The standard library sets the expectation that a nil options pointer means defaults (`slog.NewJSONHandler(w, nil)`). With the token in `APITALLY_WRITE_TOKEN`, `apitally.Init(r, nil)` is the natural env-only call, and all three reference SDKs allow setup without options. Activation is deferred to the first request, so whether `Init` keeps the caller's pointer decides whether a later `cfg.SampleRate = ...` silently applies or races. The shared design says configuration is immutable from setup.

**Recommendation:** "A nil `cfg` is equivalent to `NewConfig()`. `Init` copies the configuration, including slices; later changes to `cfg` have no effect."

**Lost:** nothing.

### S14. Re-export with wrapper functions, and pass caller skip explicitly

**Open | Low | Design sections 3, 8 and 13, lines 72, 184, 263, 284**

`var NewConfig = apitally.NewConfig` shows up on pkg.go.dev under "Variables" with no signature, in the package most users read first, and user code can reassign it. Thin wrapper functions are the normal Go re-export idiom. Any wrapper, including the framework-context `CaptureError(c, err)` wrappers the design already requires, adds a stack frame, so a plain `runtime.Caller(1)` in the root implementation records the wrapper's location instead of the user's for `StartSpan` and `CaptureError`.

**Recommendation:** framework packages define documented wrapper functions for `NewConfig`, `Shutdown`, `NewSlogHandler` and `StartSpan`. Caller-sensitive functions delegate to an internal implementation that takes an explicit skip count, so root and framework entry points both record the user's frame. Type aliases stay.

**Lost:** nothing.

### S15. "Omit catch-all method registrations" is only implementable on Echo v5

**Open | Low | Design section 9, line 204**

Gin `Any`, Fiber `All` and Echo v4 `Any` register one route per method, and `chi.Walk` explicitly skips the `"*"` method and emits each method separately (chi v5.1.0 `tree.go:871-872`). Only Echo v5 has a marker (`RouteAny = "echo_route_any"`). Anything else needs a heuristic, and a wrongly omitted endpoint is marked removed by the server. Also, Echo v5 has no `Echo.Routes`; the listing is `e.Router().Routes()`, as v0 uses.

**Recommendation:** filter `RouteAny` on Echo v5; elsewhere report per-method entries as listed, minus `HEAD` and `OPTIONS`, and say so in section 9. Fix the Echo v5 method name.

**Lost:** nothing.

## Validation record

The main reviewer re-verified, independently of the reviewers:

- **S1:** reproduced the init panic with slim v1.11.0 plus `otlptracehttp` v1.46.0 on Go 1.25.8; confirmed a canonical-data-only build has no gRPC packages or `go.mod` entries; confirmed module `go` directives.
- **S2:** read `SetBodyStream`, `ResetBody` and `closeBodyStream` in fasthttp v1.51.0 and v1.69.0 (close before install; no other public setter); `ReadCloserWithError` absent in v1.52.0 and present in v1.53.0; Fiber v2.52.15 requires fasthttp v1.51.0; user values are reset after `writeResponse` and flush (v1.51.0 `server.go:2412-2467`) and `io.Closer` values are closed on reset (`userdata.go:77-78`). The reviewer's empirical timing runs across four Fiber versions were not repeated.
- **S3:** read R11/R12 rationale and shared design sections 5-6 and 8; synchronous `OnEnd` in `End` is OTel SDK behavior already relied on by [the first review](design-review.md).
- **S4:** `slog.Record` method set in Go 1.25.8.
- **S5:** built a working `otlptrace.NewUnstarted` exporter with a custom `Client` encoding `TracesData`.
- **S7:** Fiber v3.5.0 `Ctx.Value` and fasthttp v1.51.0-v1.53.0 `RequestCtx.Value`.
- **S15:** chi v5.1.0 `Walk`, Echo v5.4.0 `RouteAny`, Gin v1.12.0 `Any`.

S6, S8, S11 and S14 rely on reviewer programs whose mechanisms follow directly from the cited source; they were checked for plausibility, not re-run. S9, S10, S12 and S13 are design-level arguments.

## Findings disposition

| Reviewer | Reported finding | Disposition |
| --- | --- | --- |
| Config/logs 1 | Slim proto registration conflict | Confirmed, S1. |
| Transport 1, 2 | Fiber stream wrapper install; `CloseWithError` floor | Confirmed, merged into S2. |
| Tracing 1, 2 | Always create span; middleware-only root designation | Confirmed, merged into S3. |
| Config/logs 3 | `MaskLogRecord` on `slog.Record` | Confirmed, S4. |
| Config/logs 2 | Public `otlptrace` conversion | Confirmed, S5. |
| Tracing 3 | Private-mode span in request context | Confirmed, S6. |
| Idiom 1 | Fiber `Locals` | Confirmed, S7. |
| Idiom 2 | Silent `Init` after routes | Confirmed, S8. |
| Config/logs 4 | Automatic slog wrapping | Recorded as S9; reopens R1/C3 with new arguments. |
| Idiom 3 | Root `internal` across modules | Confirmed mechanism, S10; frequency not demonstrated. |
| Transport 3 | `ReadFrom` and `io.Copy` | Confirmed, S11. |
| Config/logs 5 | Inactive middleware behavior | Confirmed gap, S12. |
| Idiom 4 | Nil config and copying | Confirmed, S13. |
| Idiom 5 | Function-variable re-exports and caller skip | Confirmed, S14. |
| Transport 4 | Catch-all route omission | Confirmed, S15. |
| Tracing 4 | Replace stock `BatchSpanProcessor` with a shared generic batcher | Not promoted; see below. |

Suggested discussion order: S1; S2; S3 with S6; S4; S5; S9; S7-S8; S10-S15.

## Concerns not promoted to findings

- **Replacing the stock `BatchSpanProcessor`** with the log batcher and an Apitally-owned span entry type would avoid per-span copies on the ending goroutine and make the implicit unsampled-span filter explicit. The gain is modest, the design already accepts the record-only behavior, and with S5 the exporter consumes `ReadOnlySpan` anyway. Building export copies lazily at export time, as Python and JavaScript do, can be an implementation choice without a design change.
- **Gin split capture** (`r.Handlers = append(gin.HandlersChain{observer}, r.Handlers...)` then `r.Use(capture)`) is two lines on an exported field; `Use` rebuilds the 404/405 chains. Verified on Gin v1.9.1 and v1.12.0. Keep.
- **Response-writer forwarding** is met by v0's approach (always implement `Flush`, `Hijack`, `Push`, `ReadFrom` with graceful degradation, plus `Unwrap`) without combinatorial wrappers. `httpsnoop` cannot produce a `gin.ResponseWriter`. Chi's `WrapResponseWriter` double-counts `ReadFrom` bytes at the v5.1.0 floor and should not be reused.
- **Echo/Fiber dispatch-then-return-nil** matches Fiber's logger. `echo-opentelemetry` returns the error and predicts status with `ResolveResponseStatus`, which misses custom handler mappings; the design's choice is better for validation capture.
- **`fmt.wrapError` unwrapping** is fine; implement it by comparing `reflect.TypeOf` with a sample `fmt.Errorf("%w", ...)` captured at init, not a type-name string.
- **Validation recognition by method set**, **identity-based unset detection**, **three-way provider selection**, the **propagator rule**, **embedding `ReadOnlySpan`** (OTel's own `tracetest` does it), a **side table keyed by span ID** (worse: leaks on full-queue drops), **late-telemetry drop**, **own log pipeline and histograms**, **gopsutil**, **private HTTP client**, **spool files** and **three goroutines** were checked and are fine.
- **Process-global runtime with first-config-wins**, **`Init` with no error return**, **`NewConfig()`**, **`[]string` patterns with `(?i)`** (Go canonicalizes header names, so forced case-insensitivity is the privacy-safe choice; the migration guide should list the `[]*regexp.Regexp` to `[]string` change), **comma-ok sampling callbacks**, **`ReadOnlySpan` in callbacks**, **`StartSpan`**, **per-framework request helpers**, **`testing.Testing()`**, **diagnostics through `slog.Default()`**, **config equality via `reflect.DeepEqual`** and **converting `KindAny` values at capture** (needed for race safety regardless of S4) were checked and are idiomatic enough or already justified.
- **Fiber 5-second non-terminal hook flush** is redundant with documented `Shutdown(ctx)` but small and harmless.
