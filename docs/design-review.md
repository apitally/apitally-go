# Apitally Go v1 adversarial design review

Date: 2026-10-05
Reviewed revision: `aa1a35f` (`docs/design.md`)
Status: Review complete; 7 findings resolved, 8 await design decisions (1 high, 7 medium).

This is a companion review, not an implementation plan. Recommendations remain proposals unless a decision is recorded. Approved decisions have been applied to the Go design; findings and line references describe the reviewed revision.

## Method

Four independent reviewers examined:

1. Go idiomaticity and inappropriate transfers from Python/JavaScript.
2. Lifecycle, concurrency, provider integration and sampling.
3. Framework transport, error capture and privacy.
4. Shared-contract compatibility and implementation-planning readiness.

The main reviewer independently checked findings against the Go design, inherited specification/design, dependency source, existing POCs and focused reproductions. Duplicate findings are consolidated. Deliberate scope decisions are not treated as defects merely because another API style is possible.

During decision discussions, check the corresponding behavior in the actual reference SDK implementations before presenting options. Cross-SDK consistency is a priority; proposed Go deviations need a grounded reason.

Priorities:

- **High:** the specified mechanism can break applications or lose promised telemetry in ordinary supported configurations.
- **Medium:** a concrete contract conflict or a decision that should be settled before implementation planning.

Findings are **Open** unless marked **Resolved** below. Resolving a finding means recording the user's decision here and applying the agreed documentation changes before moving to the next finding.

## Validated findings

### R1. Slog handler identity does not prevent the known deadlock

**Resolved | High | Go-specific | Design section 9, lines 193-195**

**Decision:** retain automatic capture for independent application default handlers. Recognize original and derived standard handlers by pointer kind, full package path `log/slog` and concrete type name `defaultHandler`, with supported-Go-version coverage, and leave them unchanged. Explicit standard-handler wrappers may be used by separate loggers; global installation requires an independent underlying handler. Applied to design sections 9, 15 and 17.

An application can legitimately add common fields with `slog.SetDefault(slog.Default().With("service", "api"))`. This changes handler identity but retains the standard library's `defaultHandler`. The proposed activation test mistakes it for an independent application handler and wraps it globally. A concurrent log call during the install/restore window can permanently deadlock application logging and activation.

The explicit API also needs a safe usage contract: installing `NewSlogHandler(slog.Default().Handler())` through `slog.SetDefault` creates the same recursive logging path even without concurrent installation.

**Validation:** Go 1.27.1 `src/log/slog/handler.go:114-130` retains the standard-log output function in derived handlers. `src/log/slog/logger.go:62-75` avoids recursion by testing the concrete default-handler type, not identity. The [slog POC](../pocs/slog-default-wrapping/README.md) explicitly warns about derived handlers. Independent reruns reproduced both blocked restoration after deriving the default and blocked ordinary logging after explicit global wrapping.

**Recommended decision:** recognize and leave derived standard handlers unchanged as well as the original. The POC's narrow concrete-type recognition is an evidenced option. Document that a wrapper around a standard handler may be used by an independent `slog.New(...)` logger, while global installation requires an independent underlying handler. An explicit-only capture API is a simpler alternative if automatic wrapping is not worth these ownership rules.

**Acceptance:** a default logger with `With` attributes remains usable during activation; examples cannot install a recursive default wrapper.

### R2. Mandatory outermost registration prevents automatic panic capture

**Resolved | High | Go-specific | Design sections 8/13, lines 138, 156, 234, 253**

**Decision:** keep typed `apitally.Init(app, cfg)` with no return value for every framework, preserving normal serving and application-owned recovery. The SDK owns its internal registration and per-app duplicate setup. Gin places observation outside pre-existing recovery and passive panic capture inside it, retaining `gin.Default()` and `r.Run()`. Chi, Echo and Fiber use normal middleware registration at initialization; original-panic visibility depends on recovery order. Inner recovery may hide the original panic, while returned recovery errors remain eligible through existing framework channels. Without a captured error, an observed 500 does not increment captured-server-error counts. The universal original-panic capture promise is withdrawn. No recovery argument or separate user-installed capture hook is required. Applied to design sections 3, 6-8, 13, 15-17.

Following the required order in Chi, `Use(apitallyMiddleware)` followed by `Use(middleware.Recoverer)`, puts recovery inside Apitally. Recovery consumes the panic before Apitally's deferred function can see it. The client receives a 500, but Chi has no error-return channel from which Apitally can recover the exception or increment server-error counts. Default Echo recovery has the same ordering problem.

This is not the accepted custom-status approximation: the error itself disappears. The design's sentence saying Apitally inside recovery cannot capture the panic also reverses the actual nesting relationship.

**Validation:** [Chi recovery-order POC](../pocs/framework-error-handlers/chi/chi_test.go), `TestRecoveryOrderAndServerBaseline`, explicitly checks both orders. Independently rerunning the prescribed registration order produced `panic captured=false actual status=500`. Chi v5.3.2 `middleware/recoverer.go:22-45` consumes the panic; Echo v4.16.0 `middleware/recover.go:85-130` consumes it and invokes the error handler.

**Recommended decision:** choose framework-specific recovery integration and document the real registration order. A recovery exception to the outermost rule can preserve panic visibility, but must be decided together with R3. Alternatively use an explicit recovery capture hook where the framework provides one. Preserve request-state availability for ordinary user middleware.

**Approved acceptance:** two-argument initialization preserves application serving and recovery behavior. Capture follows the documented registration boundary: visible panics are captured once and re-panicked unchanged; swallowed panics are not promised. Documentation and integration tests do not require universal original-panic capture or an error count from a 500 without a captured error. This replaces the review's proposed stronger acceptance criterion.

### R3. Panic finalization precedes the recovery response

**Resolved | Medium | Go-specific | Design sections 6-8, lines 111, 123, 136, 156**

**Decision:** preserve simple setup and explicitly narrow the whole-application transport/completion promise. Ordinary response observation finishes when Apitally's handler chain returns or unwinds, after any SDK-owned error dispatch. Fiber streamed responses follow the completion lifecycle approved in R5. If outer recovery writes later, recovery headers, body, final size and custom status are not fully observed; duration excludes that later work. Visible panic unwind records the committed status or an assumed 500, without claiming a 500 was sent to the client. Incomplete bodies are omitted. Gin observes pre-existing recovery after it completes, but automatic redirects and default 404/405 writes outside middleware remain accepted gaps. Recorded as explicit Go adaptations in design sections 6-8, 13 and 15-17, so inherited shared requirements cannot impose the withdrawn guarantees.

With recovery outside Apitally, the panic is visible, but finalization in Apitally's deferred function occurs before recovery writes its response. Inferring status 500 does not supply response headers, body or size. Default-enabled response-header capture and size metrics are wrong even when the inferred status is correct; enabled body capture misses the recovery payload.

**Validation:** an independent Echo v4.16.0 default-recovery reproduction observed `size=0 content-type=""` during Apitally-style panic finalization, followed by the actual `500`, 36-byte JSON response. Echo's `middleware/recover.go:85-130` calls `c.Error` only after inner middleware unwinds. The shared design sections 6-8 require final transport attributes and error-handler responses to be observed.

**Recommended decision:** coordinate finalization with recovery/error-handler completion. If full observation is deliberately outside v1 scope, explicitly record missing recovery headers, bodies and sizes as an additional adaptation rather than claiming only custom statuses can be inaccurate. Resolve after selecting the recovery mechanism in R2.

**Approved acceptance:** the Go design explicitly bounds transport observation, response sampling, metrics and capture to the supported handler-chain boundary. Later outer recovery and Gin-generated response gaps are stated consistently in completion, capture, API, adaptations and testing sections. The shared contract permits this explicit Go adaptation; complete recovery telemetry is not an implementation requirement. This recovery limitation does not shorten the Fiber streaming lifecycle approved in R5.

### R4. `Unwrap` does not preserve direct response-writer interfaces

**Resolved | High | Go-specific | Design section 7, line 131**

**Decision:** preserve relevant direct response-writer interfaces internally, alongside `Unwrap`, and use a dedicated wrapper satisfying Gin's full writer interface. Review and reuse v0 forwarding logic; preserve SSE and supported connection-upgrade behavior without user changes. Supported alternate write paths, including Gin's `WriteString`, participate in capture and once-only counting of successfully written bytes. Applied to design sections 7 and 16.

`http.ResponseController` follows `Unwrap`, but a handler's direct `w.(http.Flusher)` or `w.(http.Hijacker)` assertion does not. A wrapper implementing only `http.ResponseWriter` and `Unwrap` breaks ordinary SSE and upgrade handlers. Gin also requires its own richer `gin.ResponseWriter` interface.

**Validation:** a wrapper over `httptest.ResponseRecorder` reported `direct Flusher=false`, while `ResponseController.Flush()` succeeded. Go 1.27.1 `src/net/http/responsecontroller.go:21-61` explains the distinction. Gin v1.12.0 `response_writer.go:23-45` requires `Flush`, `Hijack`, `CloseNotify`, `WriteString` and status methods. [The v0 wrapper](../common/response_writer.go) already forwards several interfaces explicitly.

**Recommended decision:** preserve the relevant directly asserted interfaces alongside `Unwrap`, with a Gin-specific wrapper. Alternate write paths must still participate in byte counting and capture. Review existing wrapper logic rather than replacing it with `Unwrap` alone.

**Acceptance:** SSE flushing and supported upgrades retain their baseline behavior; writes through supported alternate paths are measured once.

### R5. Fiber streaming requires a completion mechanism beyond middleware return

**Resolved | Medium | Go-specific | Design sections 6/7, lines 111-113, 132**

**Decision:** match the reference SDKs with internal response-stream observation through completion or abort. Copy retained request values before middleware returns, wrap the final stream without draining or buffering the whole stream, count consumed bytes and use fasthttp's error-aware close lifecycle for once-only finalization. Preserve original close behavior and error propagation; EOF alone is insufficient for fixed-length or skipped bodies. Duration includes server-side streaming, request release waits for stream observation and SERVER span end, and stream-time telemetry remains eligible until release. Response size uses declared size or complete-consumption byte count; unknown final size after abort is omitted. Eligible streamed response payloads use the shared bounded capture rules: owned chunk copies, the 50,000-byte cap, `[BODY_TOO_LARGE]`, omission of incomplete partial buffers, decompression, masking and redaction. The initial response-stream payload exclusion is withdrawn; streamed request-body capture remains outside scope. Applied to design sections 6-8, 15 and 16; no extra setup.

**Reference-SDK check:** Python ASGI completes observation on the final body message (or final unwind); WSGI completes on iterator exhaustion or close. JavaScript Node integrations complete on response `finish` or premature `close`; .NET uses response `OnCompleted`. These cover server-side streaming rather than merely handler return. The initial discussion recommendation to narrow Fiber duration to handler time was withdrawn after checking these implementations. The approved decision above preserves cross-SDK streaming behavior.

The reviewed design deliberately excluded streamed bodies from payload capture. That exclusion did not resolve duration, known-size counting or release timing. For `c.SendStream(reader)`, fasthttp consumes the stream after Fiber middleware returns. Finalizing then ends the request before streaming starts and classifies stream-time telemetry as late.

**Validation:** an independent Fiber v2 reproduction printed `middleware returned, stream not yet read`, followed by `first stream read: middleware already finished=true`. fasthttp v1.73.0 `server.go:2621,2674` calls the handler before writing the response; `http.go:2283-2357` reads and closes the response stream. Shared design section 6 explicitly ties duration and release to streaming completion.

**Recommended decision:** specify response-stream observation and finalization on completion/error, using request values copied before context reuse. Stream wrapping is a candidate; verify its close/error semantics for fixed-length, unknown-length and aborted streams. Retain the deliberate payload-capture exclusion. Alternatively record a narrower Fiber timing contract explicitly.

**Approved acceptance:** streamed responses retain duration measurement and request state through stream completion or abort, without user setup changes. Fixed-length, unknown-length, skipped-body and aborted streams preserve their original close/error behavior and finalize once; handler return alone does not finalize the request. Enabled payload capture uses the same eligibility, cap, completeness and privacy rules as buffered response capture.

### R6. Fresh slog records still share mutable attribute values

**Medium | Go-specific | Design section 9, lines 200, 204**

Constructing a fresh `slog.Record` does not detach objects stored in `slog.Any`. A masking callback that replaces a password inside an attribute's map can modify the application's map and the original record subsequently forwarded to its handler. The stated guarantee that the callback cannot affect normal application output does not follow from value semantics.

**Validation:** a fresh record built by copying attributes retained the same `map[string]string`; masking it changed both original application data and the original JSON log output. Go 1.27.1 `src/log/slog/value.go:221-281` stores arbitrary values by reference. [The slog POC capture code](../pocs/slog-default-wrapping/capture.go) rebuilds groups but does not detach arbitrary `KindAny` objects.

**Recommended decision:** define callback ownership explicitly. Prefer converting supported attributes into an owned, resolved representation before the callback, with a specified treatment for arbitrary values. A smaller alternative is a documented contract requiring callbacks to rebuild attributes rather than mutate referenced objects. Do not imply that copying the record provides deep isolation or introduce a generic arbitrary-object cloning system.

**Acceptance:** the callback contract explains exactly what can be modified and what isolation Apitally provides, including maps, slices and grouped attributes.

### R7. Startup serialization cannot implement configuration equality

**Medium | Go adaptation with shared-contract consequences | Design section 3, line 68**

The startup `config` representation intentionally omits the write token, environment, disabled flag, endpoint and application version. Two routers differing only in those settings therefore compare equal under the proposed algorithm. A router configured for another Apitally application silently uses the first application's credentials, without the promised configuration-conflict warning. A disabled first configuration followed by an enabled one can similarly remain silently disabled.

**Validation:** [shared specification section 9.1](../../cloud/docs/sdks/spec.md), line 234, requires these omissions. Shared design section 3, line 88, requires a warning for different configuration. These rules directly contradict the proposed comparison; no runtime assumption is needed.

**Recommended decision:** compare a separate private representation of resolved configuration that includes the omitted fields. Keep credentials out of diagnostic output and telemetry. Treat callback-presence equality as its own explicit approximation; incomparable Go functions do not justify ignoring ordinary fields.

**Acceptance:** changing token, environment or disabled state produces the documented conflict diagnostic without exposing a credential.

### R8. Consumer maps need a deterministic interpretation of the ten-entry limit

**Medium | Go-specific adaptation | Design sections 9/13, lines 189, 251**

`Consumer.Attributes` is an unordered `map[string]string`, while the inherited contract keeps the first ten valid entries. Ranging over the same over-limit map on successive requests can select different subsets, change normalized hashes and emit redundant updates. Successive patches may eventually submit all surplus keys rather than consistently ignoring them. Sorting only the selected keys for hashing is too late.

**Validation:** the Go specification's range rules explicitly permit iteration order to change. A main-reviewer reproduction ranged over the same eleven-entry map 1,000 times and obtained eleven distinct retained ten-entry subsets, even after sorting each selected subset. Shared spec section 9.3 requires selection before change detection; shared design section 9 requires order-independent equality.

**Recommended decision:** keep the map API and specify deterministic ordering before validation/limit selection. Record this as Go's interpretation of "first ten" and apply it consistently to merged patches from repeated calls.

**Acceptance:** identical submitted maps normalize to the same limited patch and hash regardless of map iteration order.

### R9. Framework compatibility floors are not settled

**Medium | Implementation-readiness decision | Design sections 1/16/17, lines 36, 304, 310**

The document chooses Go and OTel floors but not minimum supported versions for all six framework modules. POCs verify recent pinned versions, while existing module requirements include older releases. Implementation planning cannot infer which behavior and compatibility promises to preserve.

**Validation:** [framework POC versions](../pocs/framework-error-handlers/README.md) include Chi v5.3.2; [the existing Chi module](../chi-v5/go.mod) requires v5.1.0. [Current CI](../.github/workflows/tests.yaml) already distinguishes minimum/latest framework testing. Shared design section 8 requires behaviorally validated version floors.

**Recommended decision:** publish minimum versions for all six integrations and retain minimum/latest SDK integration coverage. Use the tested POC versions as the evidence-backed starting point; choose lower floors only with explicit behavioral validation. This does not require duplicating the shared wire/config contract.

**Acceptance:** each module has a decided compatibility floor and a corresponding test target before its implementation is planned.

### R10. Multi-app support does not define mixed-framework behavior

**Medium | Go support decision with backend implications | Design sections 3/9, lines 68, 187**

Multiple apps share one runtime and a union of routes, but startup has a single `framework` value and Cloud chooses one route normalizer per Apitally application. A process combining Chi and Gin during a migration fits the current wording, yet the two route syntaxes cannot both be normalized correctly by that single choice. Reporting versions for multiple majors of one family is also unspecified.

**Validation:** Cloud `apitally_cloud/ingester/otlp_metrics.py:154-155` selects the normalizer per app; `apitally_cloud/utils/endpoints.py:53-61,94,105` defines different Chi/Gin rules. The main reviewer executed those functions: Chi left `/users/:id` unchanged; Gin left `/users/{id:[0-9]+}` unchanged. Shared spec section 9.1 confirms that dashboard configuration, not startup metadata, selects normalization.

**Recommended decision:** bound initial multi-app support to one framework family per runtime/application and define an actionable diagnostic for unsupported mixed registration. Define startup version reporting for supported multiple majors. Broader mixed-framework support requires an explicit shared/backend design rather than an arbitrary first-framework choice.

**Acceptance:** the support boundary and startup representation are unambiguous for multiple registered apps.

### R11. Reused SERVER spans need explicit request association at middleware entry

**Resolved | High | Go adaptation of shared lifecycle requirements | Design sections 2/4/5/6/8/13, lines 60, 91, 101, 115, 148, 240**

**Decision:** retain reuse for recording SERVER spans whose `TracerProvider()` matches the global SDK provider Apitally attached to. Associate the reused span with enriched framework request state at middleware entry, including on the first request when `OnStart` was missed. Run exclusions and request sampling once before downstream handling, reconciling any earlier processor entry. `SampleOnRequest` runs at middleware entry for reused spans; Apitally-created spans retain initialization at `OnStart` with prepared request state. Unrelated unknown spans and telemetry already completed before association are not recovered. Applied to design sections 2, 5, 6, 8 and 15. R12 below defines the new-span fallback and its parent classification.

Outer user instrumentation starts its SERVER span before entering Apitally. On the first request, Apitally registers its processor after that start, so it receives only `OnEnd`; the design explicitly drops that unknown span, violating its first-request guarantee. On subsequent requests, `OnStart` still runs before Apitally installs framework request state. Framework-data exclusions and request sampling cannot execute there as specified. Adding attributes afterward does not change what an already-invoked callback saw.

**Validation:** the main reviewer reproduced both cases against OTel v1.46.0 and v1.47.0: request one had `matched OnStart=false`; request two had `middleware state present=false` during `OnStart`. OTel SDK `trace/tracer.go:66-77` invokes processors synchronously with the original context before `Start` returns; `trace/span.go` resolves processors again at end. [The provider POC](../pocs/provider-detection/provider_test.go), `processorRegistration`, independently establishes end-without-start delivery.

**Recommended decision:** explicitly associate reused spans with request state at middleware entry, including spans whose start was missed. Run their exclusions and request sampling after framework enrichment and before downstream handling. Document the reused-span timing exception to the universal `SampleOnRequest`-in-`OnStart` rule, and reconcile any earlier processor entry without duplicate initialization. Unrelated in-flight spans can still be dropped.

**Acceptance:** the first and subsequent monitored requests under outer user instrumentation receive correct request association, exclusions and sampling without exporting unrelated spans.

### R12. Unconditional span reuse defeats the private-provider fallback

**Resolved | High | Go-specific provider ownership conflict | Design sections 2/6/8, lines 54, 111, 148**

**Decision:** private-provider mode always creates an Apitally SERVER span rather than reusing a foreign span. Retain the incoming parent context to preserve trace continuity and explicitly classify the Apitally span as the monitored request root. Its foreign local parent does not cause duplicate-SERVER conversion or unknown-parent dropping. Foreign providers and exports remain unchanged; Apitally's private-provider coverage remains SERVER-only. The same root classification applies when an incoming span is ineligible for reuse in attached-provider mode. Applied to design sections 2, 5, 8 and 15.

A foreign global provider can wrap the official SDK and produce a recording SERVER span. The provider-selection rule correctly chooses Apitally's private provider, but the middleware's unconditional reuse rule then adopts the foreign span instead of creating a private one. Apitally's processor receives neither start nor end, so the promised SERVER-only fallback exports no request span at all.

**Validation:** a recording SDK wrapper is already a fixture in the provider POC. The main reviewer reproduced the exact selection/reuse rules against both OTel v1.46.0 and v1.47.0: `global compatible=false; existing SERVER reused=true; user exports=1; Apitally exports=0`.

**Recommended decision:** make reuse conditional on integration with the span's lifecycle. In private-provider mode, create the promised private SERVER span. Define its request-root classification explicitly: retaining a foreign local parent must not accidentally trigger the duplicate-SERVER or unknown-parent drop rules. Resolve alongside R11 without changing user-owned exports.

**Acceptance:** foreign recording providers preserve their own telemetry while Apitally receives its documented SERVER-only coverage.

### R13. Fiber prefork `OnListen` runs in the non-serving master

**Medium | Go/Fiber-specific lifecycle adaptation | Design section 4, lines 87, 91**

When Fiber prefork is enabled, unconditional `OnListen` activation starts Apitally in the master process, which does not handle requests. That process emits a startup event and liveness metrics, creating a false online instance. Go does not need Python-style restoration of inherited runtime state, but Fiber still has a non-serving parent whose activation must be suppressed.

**Validation:** Fiber v2.52.15 `prefork.go:38-63` serves in the child branch and returns; the master runs `OnListen` at line 132. Fiber v3.5.0 `prefork.go:73-87,103-105` puts serving in `ServeFunc` and `OnListen` in `OnMasterReady`. The main reviewer checked both control flows. Shared design section 4, line 106, forbids activating prefork masters.

**Recommended decision:** ensure prefork masters do not activate from this hook; serving children can use first-request activation. Using first-request activation for Fiber uniformly is a simpler alternative if reliable hook gating is unavailable. No fork-state copying/restoration machinery is needed.

**Acceptance:** prefork workers can report serving instances, while their non-serving master produces no Apitally startup or liveness signal.

### R14. Closing one Fiber app stops the runtime used by other apps

**High | Go-specific shutdown ownership conflict | Design sections 3/4, lines 68, 89, 93**

Two Fiber apps in one process, such as public and administration listeners, are expressly allowed to share the runtime. Closing either app automatically calls process-wide `apitally.Shutdown`, terminating telemetry for the still-serving app and discarding its unreleased requests. Once-only activation prevents recovery. This occurs with one framework family, independently of R10's mixed-framework issue.

**Validation:** the conflict follows directly from the specified global runtime and terminal automatic hook. Fiber v2.52.15 `app.go:887-897` and Fiber v3.5.0 `app.go:1330-1347` invoke app-local hooks. Shared design section 4, line 112, expressly forbids an app-close hook stopping a runtime still used by other servers.

**Recommended decision:** reserve terminal runtime shutdown for explicit application-owned `Shutdown(ctx)`. App hooks may perform a nonterminal flush if that is useful. This is simpler than adding server-counting and ownership machinery, and keeps process lifetime under application control.

**Acceptance:** closing one monitored listener cannot disable telemetry for another; explicit process-wide shutdown still performs the final drain.

### R15. Fiber shutdown hooks cannot inherit the caller's context deadline

**Medium | Go/Fiber-specific lifecycle decision | Design section 4, line 93**

Calling Fiber `ShutdownWithContext(ctx)` does not pass that context to shutdown hooks. The automatic Apitally call therefore has no access to the caller's deployment budget. Synchronous hook execution alone does not make the final drain deadline-respecting; slow delivery can prolong application shutdown beyond that deadline.

**Validation:** Fiber v2's `OnShutdownHandler` is `func() error`; v3's pre/post hooks are `func() error` and `func(error) error`. The app shutdown methods call them without context enforcement. The main reviewer reproduced this for both versions: a 10ms caller budget plus a 60ms hook returned after about 61ms with nil shutdown error, although the context had expired.

**Recommended decision:** make explicit `apitally.Shutdown(ctx)` the deadline-respecting terminal path. If any automatic hook flush remains after R14, give it a documented independent bounded budget; do not claim Fiber's caller deadline governs it.

**Acceptance:** documentation identifies which context controls telemetry shutdown and does not imply that Fiber propagates a deadline its hooks never receive.

## Validation record

The main reviewer reran all existing POCs with Go 1.27.1 and `go test -race -count=1 ./...` in each module:

- `pocs/provider-detection`: passed.
- `pocs/export-span-copy`: passed.
- `pocs/framework-error-handlers`: all seven packages passed.
- `pocs/slog-default-wrapping`: passed.

Some POC tests assert disproved assumptions. Passing validates their recorded observations, not every design statement. No new minimum-toolchain matrix is claimed by this review.

Focused review reproductions additionally confirmed derived/default slog deadlocks, mutable slog attribute aliasing, direct Flusher loss, Chi recovery ordering, missing Echo recovery payload at inner unwind, Fiber stream reads after middleware return, unstable map selection and mixed-framework normalization. Reused-span timing and foreign-provider fallback reproductions ran against both OTel v1.46.0 and v1.47.0; the Fiber shutdown-budget reproduction ran against both supported majors. Prefork activation and multi-app shutdown were validated from dependency control flow and the written contract, not a full multiprocess SDK deployment. Dependency source and reproducible scenarios above are the durable evidence; temporary reproduction files are not required implementation artifacts.

## Findings disposition

The reviewers reported 18 findings, consolidated into 15 unique findings. Every reported finding was independently validated; the table accounts for each. Priority and wording above reflect main-reviewer assessment, not an automatic acceptance of proposed fixes.

| Reviewer | Reported finding | Disposition |
| --- | --- | --- |
| Go idiomaticity 1; transport 1 | Slog default-handler identity/deadlock | Confirmed, merged into R1. |
| Go idiomaticity 2; contract 1 | Configuration comparison via startup payload | Confirmed, merged into R7. |
| Go idiomaticity 3; contract 2 | Unordered consumer-map selection | Confirmed, merged into R8. |
| Contract 3 | Framework version floors | Confirmed as planning-readiness decision R9, not a demonstrated runtime defect. |
| Contract 4 | Mixed-framework support | Confirmed support/normalization conflict R10. |
| Transport 2 | Recovery order loses panic | Confirmed R2. |
| Transport 3 | Recovery response not yet observed | Confirmed R3; separate from accepted custom-status approximation. |
| Transport 4 | ResponseWriter optional interfaces | Confirmed R4 for the stated Unwrap-only mechanism. |
| Transport 5 | Fiber stream completion | Confirmed R5; user additionally approved shared bounded response-stream payload capture. |
| Transport 6 | Slog attribute aliasing | Confirmed ownership-contract gap R6; generic deep copying is not recommended. |
| Lifecycle 1 | Reused SERVER span association | Confirmed R11. |
| Lifecycle 2 | Foreign-span reuse defeats fallback | Confirmed R12. |
| Lifecycle 3 | Fiber prefork master activation | Confirmed R13 from both framework implementations. |
| Lifecycle 4 | App shutdown terminates shared runtime | Confirmed R14. |
| Lifecycle 5 | Hook cannot receive shutdown budget | Confirmed R15. |

Suggested discussion order: R1; R11-R12; R2-R3; R4-R6; R13-R15; R7-R10. Related findings stay adjacent so one decision does not contradict another.

## Decision log

Entries are chronological; later decisions settle or supersede earlier proposals. Decision 8 is the current R2-R3 outcome: simple two-argument initialization with explicit recovery-dependent observation limits.

1. **R1 resolved:** user selected automatic capture with concrete-type recognition of original and derived standard handlers. Standard handlers remain unchanged; explicit global wrappers require independent underlying handlers. Design sections 9, 15 and 17 updated. Focused slog POC tests (`TestCaptureMetadataAndIdempotence`, `TestNoWindowAlternatives`, `TestConcurrencyWindow`) rerun with `-race -count=1` and passed.

2. **R11 resolved:** user selected compatible span reuse with explicit request association at middleware entry. Reuse requires provider matching; exclusions and request sampling run once after framework enrichment and before downstream handling. Apitally's own span-start path is unchanged. Design sections 2, 5, 6, 8 and 15 updated. Official SDK v1.46.0 and v1.47.0 source and race-enabled processor-registration POC runs confirm synchronous start callbacks and end-without-start delivery.

3. **R12 resolved:** user selected retention of the incoming parent when Apitally must create its own SERVER span. Private-provider mode always creates that span, classified as the monitored request root independently of a foreign parent. Design sections 2, 5, 8 and 15 updated. Provider selection and span lifecycle ownership were checked against official SDK v1.46.0 and v1.47.0 source alongside R11.

4. **Setup API decision during R2-R3:** user approved SDK-owned `apitally.Setup` as the primary entry point, rather than native registration of Apitally middleware. Per-framework typed setup owns component registration, ordering and duplicate installation. Design sections 3, 4, 8, 9, 13 and 15 updated. Final framework-specific signatures, recovery integration and Gin's outer transport boundary remain undecided; R2-R3 are not marked resolved.

5. **Setup name decided:** after comparing the sibling v1 SDKs (Python `apitally.init`, JavaScript `useApitally`, .NET `AddApitally`), user selected `apitally.Init`. This supersedes the provisional `Setup` name in decision 4; SDK-owned setup remains approved. Design sections 4, 8, 9, 13 and 15 and R2's discussion decision updated. Final signatures remain undecided.

6. **Gin setup and observation scope decided:** user selected simple v0-style serving rather than an outer HTTP wrapper. Gin's `Init(app, cfg)` returns no value and retains `gin.Default()` and `r.Run()`. The SDK owns split panic capture and response observation around pre-existing recovery. Actual recovery responses are observed; automatic redirects and engine writes outside the middleware chain are accepted observation gaps. Design sections 6, 8, 13, 15 and 17 updated. R2-R3 remain open for the other frameworks and the unrecovered-panic contract.

7. **Recovery setup proposals rejected:** user rejected both an optional recovery middleware argument to `Init` and separate user-installed panic-capture wiring for Chi/Fiber. No replacement recovery mechanism or narrower panic-capture guarantee is approved yet. Existing v0 source confirms that single-middleware setup had panic visibility and recovery-response timing limitations; those are not new OTel requirements.

8. **R2-R3 resolved by scope decision:** user approved simple two-argument `Init(app, cfg)` and requested removal of stronger incorrect v1 promises before implementation. Original-panic capture and recovery-response observation depend on middleware ordering, except for Gin's supported internal split around pre-existing recovery. Later outer recovery and Gin-generated response gaps are explicit Go adaptations; response sampling and duration use the supported observation boundary. Design sections 3, 6-8, 13 and 15-17 updated, including test expectations. This settles the pending recovery questions in decisions 4-7. R5's Fiber stream-completion contract remains open.

9. **R4 resolved:** user selected explicit internal interface forwarding alongside `Unwrap`, with a Gin-specific writer and correctly observed alternate write paths. Design sections 7 and 16 updated. Checked v0's response-writer forwarding and Gin v1.12.0's full writer interface and `WriteString` implementation. No public setup changes or additional API requirements.

10. **Reference checks required before questions:** user requested checking the other SDKs before presenting design options, because consistency is a priority. R5's initial handler-duration recommendation was withdrawn: actual Python, JavaScript and .NET implementations coordinate observation with server-side response/stream completion. Verified Python `shared/asgi.py` and `shared/wsgi.py`, JavaScript `requestObservationNode.ts`, and the reference .NET completion path. R5 remains open; no narrower Fiber streaming contract is approved.

11. **R5 resolved:** user selected internal streaming-completion observation consistent with Python, JavaScript and .NET. Duration includes fasthttp's server-side stream lifecycle; copied state survives middleware return, completion/error-aware close finalizes once and release waits for SERVER span end. Streamed bodies remain uncaptured, with no additional setup. Design sections 6-8, 15 and 16 updated. Verified fasthttp's fixed/unknown-length write and close paths, including separate `Close` and `CloseWithError` notifications; implementation tests must cover completion, skipped bodies and aborts.

12. **Fiber response-stream payload capture approved:** user selected the same bounded response capture as Python and JavaScript, superseding decision 11's retained payload exclusion. Copy only bytes consumed by fasthttp, keep at most the shared cap, discard incomplete partial buffers and apply the shared privacy pipeline. Design sections 7, 15 and 16 and R5's decision/acceptance updated. Streamed request-body capture remains excluded; no extra user setup.

Remaining recommendations are proposals. Decisions authorize design documentation changes only, not SDK implementation.

## Concerns not promoted to findings

- Process-global runtime and first-configuration ownership are deliberate shared constraints. Their concrete integration consequences still need to satisfy the documented multi-app behavior.
- `NewConfig()` is a workable explicit-default API; a zero `SampleRate` deliberately means metrics-only. Functional options are not required merely to look more Go-like.
- Explicit contexts, framework helper wrappers and the Gin context adaptation are appropriate and evidenced by the POCs.
- Embedding `sdktrace.ReadOnlySpan` works at the tested dependency floor and preserves export-copy isolation under its read-only contract.
- Root `internal` packages may be imported by framework modules within the same import-path subtree; separate modules do not invalidate this layout. v0-to-v1 requires no `/v1` or `/v2` suffix.
- The private log pipeline, slim protobuf types, slog-only capture, invisible pre-bound attributes, explicit shutdown, deferred Sentry support and explicit validation helper are justified choices, not accidental cross-language copying.
- Fiber streamed request-body capture remains excluded, and Echo/Fiber return nil after manual error dispatch. R2-R3 record accepted panic-visibility and recovery-response limitations, including custom-status approximation. R5 preserves the reference SDKs' streaming-completion lifecycle and bounded response-stream payload capture internally.
- The Go document can inherit complete shared wire, metrics and option contracts without copying their tables. Existing v0 CI/release files needing implementation changes are not independent design defects.
- OTel processor registration is concurrency-safe. R11 concerns lifecycle events and missing request association, not an unsafe registration API.
- Record-only spans, late goroutine telemetry, global-provider replacement after activation and provider setup during package initialization already have explicit limitations in the design.
- Propagator extraction needs reuse-aware ordering, but the document does not require extraction before reuse. That potential implementation mistake was not promoted to a separate design defect.
- Lack of automatic process-exit flushing is explicitly accepted; adding signal interception or unreliable exit hooks is not recommended.
