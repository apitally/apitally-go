# Request root designation POC

## Question

Does "option 1" work, and how much code does it need? Option 1 keeps reuse of an outer user-owned SERVER span (parity with Python), but simplifies the rules from design sections 2, 5, 6 and 8 (R11, R12):

1. Only the middleware designates request roots. It reuses the incoming span if it is a recording SERVER span whose `TracerProvider()` is the provider Apitally's processor is registered on (attached user SDK provider or Apitally's own global provider); otherwise it creates a SERVER span with that provider as a child of the incoming context. Private mode needs no special case.
2. The span processor never designates roots. `OnStart` only links spans whose local parent is already known; unknown spans get no entry and are dropped. No reconciliation, no R12 exceptions.
3. Exclusions and `SampleOnRequest` run once in the middleware, at the same point for both paths.
4. Unchanged: release waits for both transport observation and root span end, in either order; Apitally's response attributes go on the live span only when Apitally created it, otherwise only on the export copy.

**Answer: yes, with two extra rules the proposal does not state** (nested monitored requests, and keeping request-map entries until release), plus one recommended rule for `SampleOnRequest` input. The core logic is about 220 lines. All 17 scenarios pass under `-race`.

## Versions and reproduction

darwin/arm64, Go **1.25.8** (cached toolchain, `GOTOOLCHAIN=local`), module `go 1.25.0`:

| Module | Version | Note |
| --- | --- | --- |
| `go.opentelemetry.io/otel`, `sdk`, `trace` | v1.46.0 | design floor |
| `otelhttp`, `otelgin` | v0.71.0 | newest declaring `go 1.25.0` (v0.72.0 needs Go 1.26 and OTel v1.47.0) |
| `github.com/gofiber/contrib/v3/otel` | v1.2.4 | newest declaring `go 1.25.0` (v1.2.5+ need Go 1.26) |
| `github.com/gofiber/fiber/v3` | v3.5.0 | fasthttp v1.73.0 |
| `github.com/gin-gonic/gin` | v1.12.0 | |

```sh
cd pocs/request-root-designation
GOTOOLCHAIN=local GOWORK=off go test -race -count=1 -v ./...
ROOTPOC_SCENARIO=7-completion-order GOTOOLCHAIN=local GOWORK=off go test -race -count=1 -run '^TestScenarios$' -v ./...
```

Full output: [results-otel-1.46.0-go-1.25.8.txt](results-otel-1.46.0-go-1.25.8.txt). Five further full `-race` runs passed. OTel v1.47.0 and Go 1.26/1.27 were not run (no toolchain available offline).

Each scenario re-executes the test binary in a fresh process, as `pocs/provider-detection` does, because activation reads and sets OTel globals and compares against the provider captured at package initialization. Within one process, one Apitally runtime serves all apps, like the process-global SDK. The user's pipeline uses `WithSyncer(tracetest.NewInMemoryExporter())`; "Apitally's exporter" is a synchronous function receiving each released request (export copies plus the event that released it). Requests go through real `otelhttp`, `otelgin` and Fiber's `contrib/v3/otel` middleware, real Gin and Fiber apps (`app.Test`, which returns only after fasthttp's user-value reset and therefore after Apitally's completion `Close`), and `httptest` for net/http and Gin.

## Implementation

| File | Contents |
| --- | --- |
| `activation.go` | `sync.Once` activation with the three provider modes (attached via `RegisterSpanProcessor`, owned via `otel.SetTracerProvider` when the global is identical to the init-time value, private otherwise) and the fallback sampler. |
| `processor.go` | Span processor and request map keyed by span ID. `OnStart` links children of known local parents; `OnEnd` routes known spans to their request. |
| `designate.go` | `Begin`: activation, exclusion, reuse-or-create, recording check, `SampleOnRequest` (fail-open) and ratio test, registration. `Finish`: response attributes on the live span only when created, observation, `End` when created. |
| `release.go` | Per-request state; release when both observation and root end have happened, in either order; export copies; removal of all entries at release. |
| `exportcopy.go` | Export copy embedding `ReadOnlySpan`, overriding `Attributes()` and `SpanKind()`; attribute merge. Duplicate SERVER rule applied here: any non-root member with kind SERVER is exported as INTERNAL. |
| `semconv.go` | Request/response attribute builders and a header carrier (shared glue). |
| `nethttp.go`, `gin.go`, `fiber.go` | Framework glue. Fiber completes through an `io.Closer` stored with `RequestCtx().SetUserValue`. |

Simplifications: no resource override, payload stash, logs, metrics, `SampleOnResponse`, BSP or response-body capture; Fiber unknown-length streams report size 0 instead of using the stream wrapper; the created path extracts W3C `traceparent` only when the incoming context has no valid span (not exercised by a scenario).

## Scenarios and results

All PASS.

| # | Scenario | Key observation |
| --- | --- | --- |
| 1 | No outer instrumentation, attached SDK provider (net/http, Gin, Fiber) | Apitally creates one SERVER span; the user's exporter and Apitally both get it (same span ID) plus the handler child. |
| 2a | Outer `otelhttp`, attached, requests 1 and 2 | Activation runs inside request 1, after the outer span started (mode `""` before, `attached` after). Both requests: exactly one SERVER span in the user export; Apitally exports the same span ID with the handler child. The copy has `http.route=/work/{id}`, status and `http.response.header.content-type`; the user's exported span has neither route nor header. Released on root end. |
| 2b | Outer `otelgin`, attached | Same. `otelgin` sets its own `http.route`; Apitally's header attribute is on the copy only. |
| 3a-c | Owned mode (global unset) + outer `otelhttp` / `otelgin` / Fiber OTel | Request 1: the outer span is non-recording (started on the unset global), so Apitally creates the root. Requests 2-3: the outer span comes from Apitally's own provider (per-request global lookup in `otelhttp`, cached delegating tracer in `otelgin`/Fiber) and is reused. Exactly one SERVER span per release; handler span under it. |
| 4 | Private mode: foreign wrapper around a recording SDK provider + outer `otelhttp` on it | Apitally creates its own private SERVER span as a child of the foreign span, with no special processor rule. Foreign export unchanged (outer SERVER + handler span, no Apitally span). **S6 confirmed:** the handler span created through the foreign global is parented to Apitally's private span, which the foreign backend never receives (dangling parent). |
| 5 | Inner `otelhttp` / `otelgin` / Fiber OTel inside Apitally | Inner SERVER span linked to the request and exported to Apitally as INTERNAL; the user export still has two SERVER spans (unchanged by option 1). Handler span under the inner span is included. |
| 6a-c | User sampler drops: `ParentBased(NeverSample)`, `TraceIDRatioBased(0)`, record-only | 24 requests per sampler across all frameworks, with and without outer instrumentation, including panics: no Apitally export, no user export, 0 map entries. A non-recording created span is never registered, because it would never reach `OnEnd`. Record-only roots are registered, reach `OnEnd`, and release nothing. |
| 7 | Fiber with outer Fiber OTel; net/http opposite order | Fiber reused: the user's span ends at handler return; release happens at the completion `Close` (`last=observation`). The copy carries Apitally's status 200, size 11 and header; the user's span lacks the header. A span started inside `SendStreamWriter` after the user span ended is still included (parent is the reused root). net/http with outer `otelhttp`: observation completes first, release on outer span end (`last=root-end`). |
| 8 | Excluded `/healthz` | Nothing reaches Apitally on either path, 0 map entries. **Decision: the created path does not create a span for excluded requests.** Consequence: without outer instrumentation, a handler span of an excluded request is a parentless root in the user's backend. With outer instrumentation the user's trace is untouched. |
| 9 | `SampleOnRequest` | Called exactly once per request on both paths and all frameworks; `(0, true)` drops the request from Apitally only (user export unchanged), 0 map entries. Inputs below. |
| 10a-b | 600 concurrent requests (6 apps, created and reused paths) in attached and owned mode, first wave includes activation | Mix of OK, panic recovered outside Apitally (net/http recover, `gin.Recovery`, Fiber `recover`), excluded, sampled out, and a goroutine span started after release. 360 releases, each with exactly one SERVER root; 120 with status 500; late spans dropped; 0 map entries after requests and after late spans. No race reports. |
| 11 | Nested monitored request (net/http in-process sub-request, Gin `HandleContext`) | Two releases, each with one SERVER root, inner root a child of outer root, 0 map entries. **Fails without an extra rule** (see findings): 1 release and 1 leaked entry. |

`SampleOnRequest` input (scenario 9). The callback receives the live root span with Apitally's request attributes overlaid (an `exportCopy` over the live span):

| Path | Attributes visible |
| --- | --- |
| Created (all frameworks) | Apitally's start attributes: `http.request.method`, `url.path`, `url.scheme`, `user_agent.original`, plus `http.route` on Gin only (Gin resolves the route before middleware; net/http's `ServeMux` and Fiber resolve it later). |
| Reused, `otelhttp` | `otelhttp` start attributes (`client.address`, `network.peer.*`, `network.protocol.version`, `server.address`, `url.path`, `url.scheme`) plus Apitally's. No route. |
| Reused, `otelgin` | As `otelhttp`, plus `http.route`. |
| Reused, Fiber OTel | Its own set (`url.full`, `url.query`, `http.request.body.size`, `network.transport`, ...) plus Apitally's. No route. |

## Line counts

Non-blank, non-comment lines, excluding `package` and `import` blocks.

| Part | Lines | Detail |
| --- | --- | --- |
| Processor + request map | 53 | `processor.go`; includes `known` (5, needed by the nested-request rule) and a test-only `size` (5) |
| Middleware root designation | 69 | `Begin` 23, `Handle` 5, `reusable` 5, `excluded` 8, `sampleRequest` 10, fail-open callback wrapper 12, trace-ID ratio 6 |
| Release coordination | 62 | `release.go` 48 (state, either-order release, entry removal, export copies), `Finish` 14 |
| Export copy and attribute merge | 32 | `exportcopy.go` |
| **Core total** | **216** | |
| Activation (3 provider modes + fallback sampler) | 69 | not part of the proposal's delta; needed in every option |
| Framework glue | 130 | net/http 44 (incl. 18-line status recorder), Gin 20, Fiber 36, shared attribute builders and carrier 30 |
| Test scaffolding | 711 | harness 291, scenarios 420 |

Reuse-specific code inside the core: `reusable` and the `known` guard (10), the reuse branch in `Begin` (3), the `SampleOnRequest` overlay (2), and the either-order release state (`observe`, `observed`, `LastEvent`, about 10). About 25 lines, so option 1 costs roughly 25-35 core lines more than always-create (S3), not hundreds.

## Findings

**What was simple.**

- Middleware-only designation is genuinely simple. The processor is a parent lookup plus insertion under one mutex; it has no notion of SERVER, roots, providers or modes. The first-request case (processor registered after the outer span started) needs no code at all: the middleware registers the outer span's ID, and the SDK resolves processors again at `End`, so `OnEnd` arrives (2a, 10a with 120 concurrent first-wave outer spans).
- Private mode and owned mode need no code outside activation. A foreign or unknown local parent is simply unknown to the processor, and the middleware registers its own span after `Start` returns (no child can start before). R12's exceptions are unnecessary.
- No reconciliation exists because the processor never creates root entries. Exclusions and sampling run in one place.
- The duplicate SERVER rule needs no `OnStart` flag: at export, every non-root member with kind SERVER becomes INTERNAL.
- Either-order release is a two-flag state machine under the request mutex. Created spans use the same path (observe, then `End`), so no separate code exists for created spans.

**What was fiddly, and rules the proposal must add.**

1. **Nested monitored requests break the reuse rule (correctness bug, also in the current design).** An in-process sub-request through the same middleware (Gin's documented `HandleContext` redirect, or `handler.ServeHTTP` with the request context) arrives with Apitally's own outer root in its context. That span is a recording SERVER span on the selected provider, so it passes the eligibility test. The inner request then registers the outer root under a new request, overwriting its map entry; the outer root's `OnEnd` releases the inner request, and the outer request is never released and leaks its entries (verified: 1 release and 1 leaked entry instead of 2 and 0). Rules needed: **never reuse a span that already has a request-map entry**, and **a newly registered root overrides an inherited membership; release removes only entries it still owns**. With these, the inner Apitally span becomes the inner request's root, a child of the outer root. Design section 8's current eligibility text has the same hole.
2. **Request-map entries must live until release, not until each span ends.** On Fiber with a reused span, the user's span ends at handler return, but telemetry produced during the response write (`SendStreamWriter`) has the ended root as its parent. Deleting entries on `OnEnd` drops it. All of a request's entries are removed atomically at release under the map lock; this also prevents a child from being linked to a released request between lookup and insert.
3. **Only recording roots may be registered.** A created span that the user's sampler drops never reaches `OnEnd`, so registering it would leak (6a-b). The proposal says "create"; it must also say "and monitor only if recording". Record-only roots are safe to register (6c).
4. **`SampleOnRequest` input differs between paths unless normalized.** The reused live span carries whatever attributes the user's instrumentation set at start, which differ between `otelhttp`, `otelgin` and Fiber OTel and from Apitally's own set (for example, `otelhttp` omits `user_agent.original` when the header is absent and never has `http.route` at start). The POC passes an overlay of the live span plus Apitally's request attributes, so the same keys are always present. Recommend stating this; it costs 2 lines because the export copy type is reused.
5. **Exclusion on the created path.** Option 1 does not say whether an excluded request still gets an Apitally span. The POC skips creation (no recording cost, no health-check noise in the user's backend); the side effect is parentless handler spans for excluded requests in the user's backend when Apitally is the only HTTP instrumentation. Either choice is cheap; it has to be stated.
6. **Lock order.** Release runs under the request mutex and takes the map mutex to remove entries; `OnEnd` releases the map mutex before taking the request mutex. That order must be kept. No race reports with 600 concurrent requests and concurrent activation.

**Consequences of keeping reuse, independent of the rule simplification.**

- **Reused-span timing is the user's, not Apitally's.** The shared spec takes request timing from span start/end. On Fiber with outer OTel the exported root ends at handler return, before the response write that Apitally's observation (and its duration metric) includes; scenario 7 shows a child span starting after its exported root ended. With outer `otelhttp`, the span includes outer middleware time. Section 6's "duration includes the response write" is false for reused Fiber spans unless the export copy also overrides `EndTime()` with the observation time. The current design has the same gap.
- **Release depends on the user's span ending.** If outer instrumentation never ends its span, the request never releases. Always-create removes this dependency.
- **Owned-mode sampler.** To make outer spans reusable, the fallback sampler must record every SERVER span (it cannot use S3's context-marker optimization). Unrelated SERVER spans (e.g. `otelgrpc`) and excluded requests under outer instrumentation are recorded and then dropped by the processor. CPU cost only.
- **S6 is real:** private mode puts a span into the request context that the foreign backend never receives.

**Comparison with the current design text (judgement, not implemented).** R11 needs the processor to classify SERVER spans at `OnStart` and create entries for outer spans before request state exists, and the middleware to find, merge, delete (excluded or sampled out) or create that entry; entries for SERVER spans of other servers need cleanup at `OnEnd`. The two `SampleOnRequest` timings need request state passed into `Start` through the context for created spans and a second call site for reused spans. R12 needs a context marker so the processor exempts the designated private root from the duplicate-SERVER and unknown-parent rules. That is perhaps 40-60 more lines, but the real cost is more states and more leak paths; option 1 removes all of them. Against S3 (always create), option 1 costs about 25-35 core lines, plus the timing, release-dependency and owned-sampler consequences above, in exchange for one SERVER span in the user's backend instead of SERVER plus INTERNAL.

## Conclusion

Option 1 is sound and small. Adopt it with these additions to design sections 5, 6 and 8:

1. A span that already has a request-map entry is never reused; registering a root overrides an inherited membership, and release removes only entries it owns.
2. Request-map entries for all of a request's spans remain until release.
3. A root is registered only when it is recording.
4. `SampleOnRequest` receives the live root with Apitally's request attributes overlaid, on both paths.
5. State whether excluded requests get an Apitally-created span (POC: no).
6. Decide whether reused-span export copies override end time with the observation boundary, or document that request-log duration for reused spans is the user's span duration.

S6 should be resolved separately; this POC confirms its premise.
