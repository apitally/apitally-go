# Apitally Go v1 design review: consistency with .NET

Date: 2026-10-06
Reviewed revision: `91567dc` (`docs/design.md`)
Compared against: .NET v1 design, last version before removal (`apitally-dotnet` commit `998cca0^`, `docs/design.md`)
Status: Review complete; 7 findings resolved, 4 rejected.

This review lists user-observable differences between the Go and .NET designs, including performance-relevant differences and developer-experience inconsistencies, and assesses whether each is warranted by what is possible or idiomatic in Go. The shared design (`cloud/docs/sdks/design.md`) decides which side is the outlier where the two SDKs disagree. Findings and recommendations are input for discussion, not requirements. Resolving a finding means recording the user's decision here and applying the agreed documentation changes before moving to the next finding.

The current .NET public API (`ApitallyOptions`, `IApitally`, `LogRecordSnapshot`) was spot-checked and matches the compared design.

Priorities:

- **High:** ordinary supported setups silently lose promised telemetry or functionality.
- **Medium:** a concrete user-observable or performance difference that should be settled before implementation planning.
- **Low:** a documentation gap or minor inconsistency.

## Findings not judged warranted

### C1. Struct literals silently lose non-zero defaults

**Rejected | High | Design section 3, line 80**

**Decision:** no guard. Setup code that bypasses `NewConfig()` is intentionally wrong usage, and the SDK does not guard against it. The design's existing statement stands. No design change.

`&apitally.Config{WriteToken: "..."}` yields `SampleRate` 0 (metrics-only), `CaptureLogs` false and `CaptureResponseHeaders` false without any diagnostic. .NET property initializers make this impossible.

### C2. Go log records show bare messages and export attributes the server ignores

**Rejected | High | Design section 9, line 217**

**Decision:** keep exporting slog attributes as OTel log attributes. The server will be changed to store them. Already decided; not revisited.

.NET exports the rendered message (`User 42 logged in`) and deliberately omits structured values because the server stores only message, level, logger and code location. Go exports the slog `Message`, which by slog convention carries no values, plus all attributes.

### C3. Default log capture is silent when the standard library default handler is in use

**Resolved | High | Design section 9, line 211**

**Decision:** keep the standard library default handler unwrapped. When `CaptureLogs` is true, activation warns once that application logs are not captured, naming the remedies: installing an independent default handler, or setting `CaptureLogs` to false. Applied to design section 9.

Leaving the standard library `defaultHandler` unwrapped is warranted (R1 deadlock). Recording this only in a debug message is not: log capture is on by default, the application loses all application logs, and the user can act. The shared posture (section 12) reserves warnings for exactly this combination. .NET always captures `ILogger` output.

**Recommendation:** warn once at activation when `CaptureLogs` is true and the default handler is the standard library handler, naming the remedies (install an independent handler, or set `CaptureLogs` to false).

### C4. No single recovery-ordering rule across frameworks

**Resolved | High (Chi), Medium (Echo, Fiber) | Design section 8, line 147**

**Decision:** documentation prescribes one order on all four frameworks: recovery first, then `Init`, then ordinary middleware, groups and routes. Panics reach Apitally's observer with their original value and stack; the accepted R3 recovery-response limits apply. Applied to design sections 8 and 13.

.NET captures unhandled exceptions regardless of middleware order. R2 accepted ordering-dependent panic capture in Go, but the design prescribes an order only for Gin (recovery before `Init`). For Chi, Echo and Fiber it says to call `Init` before ordinary middleware and leaves recovery placement open. Recovery registered after `Init` consumes panics first: Chi's `Recoverer` leaves no captured error, so panics never count as server errors; Echo and Fiber lose the panic stack.

**Recommendation:** one documented rule for all four frameworks: recovery first, then `Init`, then everything else. This uses the accepted R3 outer-recovery limits and needs no new mechanism.

### C5. SDK diagnostics bypass the application's logging

**Resolved | Medium | Design section 12, line 245**

**Decision:** log diagnostics through `slog.Default()`, looked up at call time, with a `logger=apitally` attribute and always with `context.Background()`, so they follow the application's handler, format and level and are dropped by the log capture. Applied to design section 12.

.NET and Python route SDK diagnostics through the application's logging infrastructure, so they follow its format, sinks and levels. Go writes plain text to stderr through a private logger: lines break JSON log streams, cannot be routed or filtered, and debug output is unreachable. The stated reason (log capture never sees them) already holds because SDK records carry no request context, which the capture drop rule requires.

**Recommendation:** log through `slog.Default()` at call time with `context.Background()` and an attribute identifying Apitally.

### C6. Zero-copy file sends are not preserved

**Resolved | Medium (performance) | Design section 7, lines 136-137**

**Decision:** match .NET on Chi and Fiber. The net/http wrapper forwards `io.ReaderFrom` where the underlying writer implements it, counting returned bytes and omitting body capture for that response. Fiber's stream wrapper exposes the original file-backed stream's zero-copy method, still counting bytes and observing close, and omits body capture. Applied to design sections 7 and 15.

.NET delegates native file sends unchanged and omits their body capture. The Go net/http wrapper does not list `io.ReaderFrom`, and Fiber wraps every response stream, including `SendFile` and static files. `http.ServeFile`, `http.ServeContent` and Fiber file responses therefore lose sendfile and copy through userspace buffers.

**Recommendation:** keep the zero-copy path (forward `io.ReaderFrom` in net/http wrappers; in Fiber, keep the file reachable by fasthttp's zero-copy copy path), count bytes, and omit body capture for file sends, recorded as the same scope deviation as .NET.

### C7. Request-logging middleware is captured as application logs

**Resolved | Medium | Design section 9**

**Decision:** exclude records whose `code.function.name` starts with a fixed list of slog request-logging packages (`go-chi/httplog`, `samber/slog-gin`, `slog-echo`, `slog-chi`, `slog-fiber`), as .NET excludes framework request-log categories. The application's handler still receives them. Applied to design sections 9 and 15.

.NET excludes `Microsoft.AspNetCore.*`, `System.Net.Http.HttpClient.*` and `Yarp.ReverseProxy.Forwarder.*` categories: they duplicate the request log, crowd out the 1,000-record buffer, count against log usage and can contain unredacted query strings. The Go design does not address this. go-chi/httplog v3 logs every request with `logger.LogAttrs(ctx, ...)` using the request context, so when registered after `Init` it adds one duplicate record per request containing the raw URL.

**Recommendation:** decide explicitly; for example, drop records whose source location belongs to a short list of request-logging packages, or document the behavior.

### C8. Exception type is `fmt.wrapError` for most real errors

**Resolved | Low-Medium | Design section 8, line 182**

**Decision:** while the captured value is a `*fmt.wrapError`, follow `Unwrap` and report the first other type; keep the outermost message. Multi-error wrappers are reported as they are. Applied to design sections 8 and 15.

.NET reports the thrown exception type. Go reports the captured value's type, and wrapping with `fmt.Errorf("...: %w", err)` is ubiquitous, so unrelated errors share the type `fmt.wrapError`.

**Recommendation:** follow `Unwrap` past standard library wrapper types and report the first other type, keeping the outermost message.

### C9. All activation work blocks the first requests

**Rejected | Low-Medium (performance) | Design section 4, line 92**

**Decision:** keep synchronous activation, consistent with Python and JavaScript. The one-time cost is a few milliseconds on the first request. No design change.

First-request activation is warranted. .NET activates before serving, so its first request pays nothing. In Go, the whole activation runs inside a `sync.Once` that first requests wait on: route enumeration, the startup event, the spool probe, orphan spool cleanup in the temp directory and process-metrics setup.

**Recommendation:** only the tracing pipeline needs to be ready synchronously; run the remaining activation work on the export worker.

### C10. Documentation gaps that allow drift

**Resolved | Low | Design sections 3 and 9**

**Decision:** list the `Config` fields with the shared option names, including `AppVersion`; count log truncation in runes without splitting UTF-8 sequences; drop records whose message is empty after masking, as .NET does; serialize startup patterns as the effective pattern with the `(?i)` prefix, as .NET does. Applied to design sections 3 and 9.

- The option list is not enumerated, and `AppVersion` (shared option, present in .NET) is never named.
- The unit of the 2,048-character log truncation is unspecified. .NET counts UTF-16 code units; Go should specify runes and never split UTF-8.
- Handling of an empty message after `MaskLogRecord` is unspecified. .NET drops the record locally because the server drops empty bodies.
- Startup pattern serialization: .NET includes effective flags; Go lists the user's strings as given (line 82).

### C11. Brotli bodies on Fiber

**Rejected | Low, optional | Design section 7, line 130**

**Decision:** keep skipping `br` bodies, consistent with Python. The no-new-dependency premise does not hold across supported fasthttp versions. No design change.

.NET decodes `br`. Skipping it is warranted for net/http modules (no standard library decoder). fasthttp already depends on `github.com/andybalholm/brotli`, and Fiber's compress middleware selects `br` for browsers, so decoding `br` in the Fiber modules adds no dependency.

## Warranted differences needing prominent documentation

- **Explicit `Shutdown(ctx)`:** Go has no exit hook, and SDK signal interception would either cut short the application's graceful drain or leave a process without a handler unable to exit.
- **Request context required for linkage:** Go has no implicit context. Passing `*gin.Context` without `ContextWithFallback`, or Fiber v2's `c.Context()`, compiles but drops logs and makes `StartSpan` lose its parent.

## Warranted differences

| Area | .NET | Go | Reason |
| --- | --- | --- | --- |
| Runtime ownership | Per host through DI | Process-global, first configuration wins | Matches the shared design, Python and JavaScript |
| Configuration sources | `Apitally` section, host environment name | Code and `APITALLY_*` only | Go has no standard configuration system or environment name |
| Activation | Before server start | First request, Fiber `OnListen` | No startup hook; `Init` precedes route registration |
| Test suppression | TestServer only | `testing.Testing()` | Shared design's test-runner marker |
| Outgoing HTTP spans | Automatic `HttpClient` instrumentation | Not automatic | Replacing `http.DefaultTransport` breaks code that type-asserts it |
| Propagator | W3C by default | Registered when unset | Go's default global propagator is a no-op |
| Monitored scope | Whole application | Narrower (R2-R3) | Framework mechanics |
| Span callback type | `SpanSnapshot` | Native `ReadOnlySpan` | Matches Python and JavaScript |
| Sampling callback result | `double?` | `(float64, bool)` | Idiomatic optional value |
| Log mask callback | `LogRecordSnapshot` | `slog.Record` | Native type with value semantics |
| Logger name | Category name | `slog` | Spec section 8 rule for interfaces without logger names |
| Request helpers | Injected `IApitally`, `CaptureException`, optional parameters, null deletes | Package functions with framework context, `CaptureError`, `Consumer` struct, empty string deletes, sorted keys | Idiomatic Go; random map order (R8) |
| Request attributes | `SetRequestAttribute(key, object?)` | `SetRequestAttributes(...attribute.KeyValue)` | Idiomatic OTel Go; costs an extra import |
| Validation capture | Automatic only | Also public `CaptureValidationError`; Gin `ShouldBind*` not automatic | Go frameworks do not own validation responses |
| Returned errors | Exceptions carry stacks | Empty stacktrace; Chi relies on panics or `CaptureError` | Go errors carry no stack |
| Echo/Fiber error dispatch | Not applicable | Apitally invokes the error handler and returns nil | Avoids double error-handler invocation |
| Process gauges | Direct runtime APIs | `gopsutil/v4` | Standard library has current RSS only on Linux |
| Manual spans | No code-location attributes | Code-location attributes | No function wrapper in Go |
| Other | Endpoint summaries; streamed request bodies captured; .NET regex | None; Fiber streamed request bodies not captured; RE2 | Framework and regex-engine limits |

## Decision log

1. **C1 rejected:** user rejected guarding against struct literals; the SDK does not guard against intentionally wrong setup code. No design change.
2. **C2 rejected:** user had already decided that Go exports slog attributes and the server will be changed to store them. Not revisited. No design change.
3. **C3 resolved:** user selected a once-per-process activation warning over info-level or debug-level reporting, following shared design section 12 (actionable data loss). Design section 9 updated.
4. **C4 resolved:** user selected recovery-first ordering for all frameworks over `Init`-first ordering or per-framework trade-off documentation. POC evidence (`pocs/framework-error-handlers/README.md`): recovery outside preserves the original panic on all frameworks; recovery inside loses it on Chi and default Echo `Recover`, and leaves only a stackless converted error on Fiber. Design sections 8 and 13 updated.
5. **C5 resolved:** user selected logging through `slog.Default()` over a private stderr logger with `APITALLY_DEBUG` or a `Config.Logger` option. Verified Python uses `logging.getLogger(__name__)` and .NET uses the application's `ILogger`; JavaScript writes to stderr directly (`src/logger.ts`) because `console` is its capture surface and it has no standard logger, which does not apply to Go. Design section 12 updated.
6. **C6 resolved:** user selected preserving zero-copy file sends on Chi and Fiber over a Chi-only fix or accepting the loss. Verified Gin's `responseWriter` (embeds the `http.ResponseWriter` interface) and Echo's `Response` do not implement `io.ReaderFrom`, so those frameworks never reach `sendfile` regardless of Apitally; v0's `common/response_writer.go` also lacks it. Chi exposes the raw net/http writer. Design sections 7 and 15 updated.
7. **C7 resolved:** user selected automatic exclusion by source package over documentation-only ordering guidance. Verified `go-chi/httplog` v3 and `samber/slog-gin`, `slog-echo` and `slog-chi` log with the request context; `samber/slog-fiber` passes the Fiber context; Echo v5's built-in `RequestLogger` uses `context.Background()`; the other built-in framework loggers do not use slog. The check reuses the already resolved `code.function.name`. Design sections 9 and 15 updated.
8. **C8 resolved:** user selected skipping `fmt.wrapError` wrappers over keeping v0's rule or reporting the innermost type. Verified v0 (`internal/server_error_counter.go`), OTel Go's `typeStr` and sentry-go's top-level exception type all report the wrapper type; sentry-go additionally sends the unwrap chain, which Apitally's single-type error model cannot carry. Design sections 8 and 15 updated.
9. **C9 rejected:** user kept synchronous first-request activation. Verified Python (`shared/activation.py:78-96, 211-214`, `spool.py:80-89`) and JavaScript (`activation.ts:102-115`, `spool.ts:44-54`) run spool construction, orphan cleanup, startup route enumeration and metrics setup synchronously inside first-request activation. No design change.
10. **C10 resolved:** user approved the proposed option list, rune-based truncation and effective-pattern serialization, and selected dropping empty-message records over exporting them. Verified: Python counts code points, JavaScript and .NET UTF-16 units; Python and JavaScript export empty-body records while .NET drops them (`ApitallyLoggerProvider.cs`); Python serializes patterns as given, JavaScript as `RegExp.toString()`, .NET as `(?i)` plus pattern (`InternalEvents.cs:108-110`). Design sections 3 and 9 updated.
11. **C11 rejected:** user kept skipping `br` bodies. Verified Python decodes only gzip and deflate (`shared/config.py:148`); JavaScript and .NET decode `br` with runtime-provided decoders. Current fasthttp uses `molecule-man/go-brrr` and requires Go 1.26, while fasthttp versions at the Go 1.25 floor use `andybalholm/brotli`, so decoding would add a dependency for some users. No design change.
