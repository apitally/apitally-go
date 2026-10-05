# Provider detection POC

## Question

Can activation distinguish an official SDK provider, an unset global, and a foreign provider without affecting the application's telemetry? Also verify cached-handle delegation, propagator ownership, live processor registration, and later global replacement (design sections 2, 4, and 17).

## Versions and reproduction

Tested on darwin/arm64 with the race detector:

- Go **1.27.1**, OTel **v1.46.0** (`otel`, `sdk`, `trace`, `metric`).
- Go **1.27.1**, OTel **v1.47.0** (same modules, plus transitive `log` v1.47.0).
- Go **1.25.14**, OTel **v1.46.0** (additional minimum-toolchain check).

`go.mod` retains the Go 1.25 floor (`go 1.25.0`, normalized by tidy). The alternate module file selects OTel v1.47.0 and Go 1.26, required by that release. Switching with `-modfile` leaves the default module unchanged. Both configurations use `go.opentelemetry.io/auto/sdk` v1.2.1; see the module/sum files for exact dependencies.

```sh
cd /Users/simon/Repos/apitally/apitally-go/pocs/provider-detection

go test -race -count=1 -v ./...
go test -modfile=otel-1.47.0.mod -race -count=1 -v ./...
GOTOOLCHAIN=go1.25.14 go test -race -count=1 -v ./...
```

The test binary re-executes itself in a fresh subprocess for each of 15 scenarios. The child inherits race instrumentation. Providers use `AlwaysSample` and a synchronous in-memory exporter to make export assertions deterministic; fallback sampling and SDK activation suppression are outside this POC. A single scenario can be run with `APITALLY_PROVIDER_POC_SCENARIO=explicit-noop go test -race -count=1 -v`.

Full evidence:

- [OTel 1.46 / Go 1.27](results-otel-1.46.0-go-1.27.1.txt)
- [OTel 1.47 / Go 1.27](results-otel-1.47.0-go-1.27.1.txt)
- [OTel 1.46 / Go 1.25](results-otel-1.46.0-go-1.25.14.txt)

## Results

Identical behavior in all three configurations. Every test passes by asserting the observed behavior, including failures of the proposed detection method. PASS/FAIL below refers to the design expectation or proposed technique, not test-suite status.

| Case | Result and evidence |
| --- | --- |
| Unset global | **PASS:** probe from `context.Background()` has invalid context. Dynamic type is `*global.tracerProvider`; its element package path is `go.opentelemetry.io/otel/internal/global`. Registering our provider works. |
| Official SDK global | **PASS:** `*sdktrace.TracerProvider` assertion succeeds; attach a processor without probing or replacing the provider. |
| Recording foreign global | **FAIL (side effects):** SDK-wrapping foreign provider is correctly classified, but ending the probe causes **1 OnStart and 1 exported span** in the user's pipeline. Type-based selection preserves the global and uses a private provider. |
| Explicit `noop.NewTracerProvider()` | **FAIL (classification):** invalid probe context looks unset. Dynamic type is `noop.TracerProvider`; type-based selection preserves it as foreign. |
| Nonrecording foreign wrapper | **FAIL (classification):** also returns invalid context. Special-casing the official noop type cannot fix arbitrary foreign providers. |
| Probe never ended | **FAIL (side effects):** **1 OnStart, 0 OnEnd, 0 exports**, also 0 exports after ForceFlush. Shutdown does not supply OnEnd. A processor's start accounting remains unmatched. |
| Unset global, valid parent | **FAIL (request-context probe):** nonrecording span inherits the valid parent context. A probe on the first request's context can falsely report foreign. |
| Tracer/provider cached before activation | **PASS:** both delegate after registration; **2 children + SERVER** exported with correct parent IDs and scope/version. A span already started before registration remains noop. Current global becomes the actual SDK pointer; the cached default retains its internal type. |
| Unset propagator | **PASS:** internal `*global.textMapPropagator` initially has 0 fields. After W3C registration, cached and current handles both have 3 fields, inject `traceparent` and `baggage`, and extract the matching remote parent and baggage. |
| Explicit empty propagator | **FAIL (`Fields()` detection):** an explicitly installed empty composite also has 0 fields. Dynamic-type detection preserves it. |
| Explicit W3C propagator | **PASS:** 3 fields; preserved when installing our provider. |
| Explicit baggage-only propagator | **PASS:** 1 field; preserved. Nonempty fields do not imply trace-context support. |
| Live `RegisterSpanProcessor` | **PASS:** a pre-registration span gives the new processor **OnEnd without OnStart**. Child OnStart receives the parent context and a custom request-context value. **8 workers, 4,008 spans, 16 registrations** complete with no race report; the first added stress processor sees 4,000 starts and 4,008 ends. |
| App replaces globals after activation | **PASS (documented consequence):** latest provider wins for new global tracers, but pre-activation tracers/provider and activation-time tracers remain bound to our provider. **4 spans exported by ours, 1 child by the user's new provider**, with cross-provider trace/parent IDs preserved. Cached default propagator likewise stays with its first delegate. |
| App restores cached default provider handle | **FAIL (type as universal state detector):** after setting an SDK then restoring the old default handle, the global has the internal default type but delegates to that SDK. The probe exports a user span. |

The foreign fallback test also starts a child via the global under the private SERVER span: our processor receives only SERVER, while the recording foreign pipeline receives the child with the correct parent ID.

Datadog was **not executed**. Read-only inspection of [dd-trace-go v2.10.1](https://github.com/DataDog/dd-trace-go/blob/v2.10.1/ddtrace/opentelemetry/tracer_provider.go) found a foreign `*opentelemetry.TracerProvider`; construction starts Datadog's tracer. Export tests need a fake HTTP agent and Datadog payload handling rather than an OTel in-memory exporter. The real recording SDK wrapper suffices to demonstrate the probe problem without that dependency tree.

## Conclusion and design proposals

**The probe is neither reliable nor side-effect-free.** Not ending it avoids normal end-time export, but does not undo sampling or OnStart. Invalid context means no usable trace context, not an unset global.

For the design's normal startup sequence, the most robust tested side-effect-free option is:

1. Assert `*sdktrace.TracerProvider` and attach additively.
2. Recognize the default placeholder with `reflect.TypeOf`, checking pointer kind plus element **PkgPath and Name** (`tracerProvider`). Unknown types take the foreign/private-provider path.
3. Only in the default case install our global provider; install W3C propagation only if the propagator is also the exact internal default type (`textMapPropagator`). Preserve explicitly configured empty propagators.
4. Treat explicit noop as **foreign**: warn once, retain its global, and use our private SERVER provider.

These are **proposals**, not edits to `docs/design.md`. Replace section 2's probe criterion and unconditional propagator installation with the above, and record section 17's negative probe result. Retain the in-flight OnEnd handling and first-request activation described in sections 2 and 4. Document that later global replacement splits export pipelines and does not retarget already cached handles.

There is **no supported public is-set API**. Reflection uses private implementation names, so keep compatibility tests and fail conservatively on unknown types. The restored-handle case shows that type is not an absolute ownership guarantee. The default tracer also contains an eBPF auto-instrumentation path; attached auto-instrumentation was not tested. If ownership must be guaranteed for every setup, an explicit application ownership signal or an upstream public API is needed. Comparing an early-captured provider's identity alone is insufficient if application initialization already installed a provider.

Implementation evidence: OTel's [global state](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/internal/global/state.go), [tracer delegation/default spans](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/internal/global/trace.go), and [SDK processor registration](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/sdk/trace/provider.go). The relevant tracer/propagator behavior is unchanged in v1.47.0.
