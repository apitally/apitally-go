# Agent guidance

## Checks

- Verify code changes with the Makefile targets, never with hand-picked subsets of them: `make check` (build, `go vet`, `gofmt`, `go mod verify` and `go mod tidy` in every module) and `make test` (`go test -race` in every module). Both cover the root module and every framework module; a change to the root module can break any of them.
- Run both on the Go 1.25 floor toolchain: `GOTOOLCHAIN=go1.25.14 make check test`. With each module's floor dependency versions this matches CI's floor job, and a `go 1.25` directive does not stop code from calling standard library APIs added in later Go releases. CI also runs Go 1.26 and 1.27 with the latest dependencies, and every framework module at its floor and latest framework release.
- For documentation-only changes, review the diff and run `git diff --check`; reserve the Makefile targets for code, configuration, or test changes.

## Code style

- Write the least amount of code that gets the job done.
- Write modern, idiomatic Go using only what Go 1.25 provides, and only OTel Go and framework APIs available at the declared minimum versions. A newer API is a floor raise, which is a design decision, not an implementation detail.
- Code is `gofmt`-formatted and `go vet`-clean. Fix diagnostics instead of suppressing them.
- The root package declares only the public types and `NewConfig`. Implementation lives in the root module's `internal` package. Framework packages contain only the framework integration (`middleware.go`) and the shared public API (`sdk.go`): type aliases of the root types and documented wrapper functions that call `internal`, never function variables. Identifiers in `internal` are exported only when another package uses them.
- Every dependency of the root module lands in every user's build. Recognize optional third-party types by method set, never by importing their packages.
- SDK failures never reach the application. Recover panics in SDK work and in user-supplied callbacks, report them through the SDK's diagnostics logger, and fall back to the documented safe behavior. Application panics the SDK observes are re-panicked unchanged; application errors are dispatched as the framework would.
- Every goroutine the SDK starts recovers its own panics and stops when the SDK shuts down: an unrecovered panic in any goroutine terminates the host process.
- Functions that perform I/O or wait take a `context.Context` as their first parameter and honor its deadline.
- Avoid `unsafe` and reflection on dependencies' unexported fields. Where one is unavoidable, isolate it in a single function that verifies the layout it relies on and fails safe when it changes.
- Declaration order within a file is deliberate, not accidental: exported entry points first, unexported helpers after, so the file reads top-down.
- No single-use helper functions unless extraction meaningfully improves readability at the call site.

## Naming and wording conventions

- Use plain, precise English. No invented shorthand, metaphors, or informal jargon.
- A word qualifies only by referring to an actual thing in this codebase or its dependencies, never by sounding technical: "SERVER span" refers to OTel's `trace.SpanKindServer`.
- Follow Go naming: MixedCaps, initialisms in consistent case (`spanID`, `URL`, `HTTPStatus`), no `Get` prefix on getters, and no package-name stutter (`cache.Store`, not `cache.CacheStore`).
- Prefer a longer clear name over a compact clever one.
- Vague verbs need an object or a from/to: not `resolve` but `resolveEnv`.
- Boolean predicates read as questions: `is`/`should`/`has` prefixes (`isAllowedContentType`, `shouldCaptureRequestBody`). Never name a predicate as an imperative command.
- The name states what the function actually does, including its outcome: a function that only logs a warning is `warnIf<Condition>`, not `check<Thing>`.
- One concept, one name across packages and modules. Names align with the configuration option they implement.
- When renaming a function, rename its associated constants to match.
- Public API names, including configuration field names, are stable; naming improvements are internal only.

## Comments

- Comments are sparse and concise (one or two lines). A comment states something the code cannot: a constraint, an external system's behavior, or the reason for a choice. It explains the WHY, never narrates the WHAT; a comment that restates the code below it does not get written.
- Name the real component (the env var, the OTel type, the framework version behavior), never a metaphor.
- No historical references: nothing about the 0.x SDK, "previously", or "ported from". Comments describe the present code only.
- No references to planning or design documents. Every comment stands alone against the code and its dependencies; a comment that needs a rationale states the rationale itself.
- A comment sits next to the code it justifies and stays accurate about what that code covers.
- Doc comments are for the public API, where users read them on pkg.go.dev and in their editor, and describe behavior from the user's point of view. Re-exported wrapper functions carry their own doc comments. Exported identifiers in internal packages get a doc comment only when it states something the name cannot.

## Testing

### What gets tested

- A test may only fail when user-observable behavior regresses against a contract. Documented gaps, internal mechanisms, and constants are never pinned; decisions without a user-observable failure mode are enforced in review, not tests.
- Every test needs an important reason to exist: it pins a spec requirement, a settled design decision, or a behavior a plausible change would silently break. Tests that restate the implementation, or assert theoretical edge cases no real deployment hits, do not get written.
- Test only the SDK's own code. Never write tests that assert what OpenTelemetry, a framework, or the standard library does on its own; dependencies appear in tests only as the environment the SDK's behavior is observed in.
- Never replace the SDK's own types or functions with test doubles. Substitute only process boundaries: export to a local stub OTLP endpoint (`httptest.Server`), or read exported payloads in-process.
- Prefer one integration test proving a flow end-to-end over several micro-tests asserting its intermediate steps.
- Do not multiply a scenario into parameter variants; table-driven tests are for genuine input tables.

### Layout and naming

- Tests sit next to the code they test, one test file per source file, named after it (`<name>_test.go` tests `<name>.go`), never after scenarios. Each framework module holds the integration tests that drive a small real app through `httptest`.
- Test function names are present-tense behavior statements readable without the test body, with no "Should": `TestUnmatchedRequestExportsServerSpanWithoutRoute`. Subtest names follow the same rule. Name the observable behavior, not the mechanism or an internal codename.
- Scenarios shared across frameworks use identical test function names in the same order in every framework module, and follow the Python suite's scenario names where they apply.
- Test order within a file is deliberate: core behavior first, then edge cases, failure paths, and shutdown last.

### Coverage ownership

- Every behavior is asserted in exactly one place: the lowest layer that can observe it. The root module's tests own core semantics; framework modules own integration behavior, wiring, and the canonical cross-framework scenario set, which is the only sanctioned duplication. Two tests pinning the same contract outside that set is a defect.
- Helpers stay consolidated in one internal test-support package shared by all modules: extending an existing helper always beats adding a sibling.

### Isolation, timing, and assertions

- Every test passes under `-race`.
- Tests that touch process-global state (SDK state, OTel globals, `slog.Default`, environment variables) do not call `t.Parallel`. They reset that state through shared test helpers registered with `t.Cleanup` and set environment variables only with `t.Setenv`. Never weaken a production guard to make a test pass; tests bypass guards only through internal test hooks.
- No wall-clock sleeps and never wait for a real export interval. Call worker cycles directly, or run timer-driven code in a `testing/synctest` bubble.
- Integration tests read responses to completion and shut the SDK down before asserting on exports; shutdown flushes all pending telemetry.
- Assertions are exact by default: exact counts of spans, log records, and metric data points, full attribute equality, decoded OTLP payloads. No golden files or snapshots. Use testify's `require` for preconditions and `assert` for the remaining checks.
