# v1 draft code review

Review of the first v1 draft on the `v1` branch (commit `3784e27`), covering correctness, concurrency, export pipeline robustness, framework integrations, spec conformance, structure and naming, and tests. `GOTOOLCHAIN=go1.25.14 make check` passes, staticcheck is clean, and all module suites pass under `-race`.

Each finding has an ID, a status (`open`, `fixed`, `rejected` or `deferred`) and a class. Class a is a mechanical fix: minimal, unambiguous and safe to apply without a decision. Class b needs a decision. Locations and identifier names refer to commit `3784e27`; the fixes for S1, S2 and S9 renamed some of them (for example `RequestState.CaptureError` is now `CaptureReturnedError`, and `FinishObservation` is now `finishObservation`). Update the status as findings are addressed, and add a short note for anything rejected or deferred.

## High

### H1. Fiber requests never complete when fasthttp does not reset the request context

Status: fixed
Class: b (needs decision)
Note: Option 1. Buffered responses and streams of known length complete at handler return; only wrapped streams of unknown length wait for `Close`. Added `TestRequestServedThroughAdaptorIsExported` (fiber-v2, fiber-v3) and updated design sections 6, 7, 8 and the summary table.

- **Where:** `internal/fasthttp.go:36-37,96`, `internal/requests.go:114` (register) and `:233` (the only `remove`).
- **Problem:** Observation completes only when fasthttp closes the Apitally `io.Closer` user value on `RequestCtx` reset. Fiber v2's `adaptor.FiberApp`/`adaptor.HTTPHandler` and `aws-lambda-go-api-proxy/fiber` build a `fasthttp.RequestCtx` that is never reset; the v3 adaptor pools it and resets it only when the next request reuses it. `FinishObservation` then never runs: no metrics, errors or spans, the created SERVER span never ends, and each request stays in `requestRegistry.entries` (with up to 1,000 buffered spans) until `Shutdown`. No warning is logged.
- **Scenario:** a Fiber app on AWS Lambda through the awslabs proxy, or mounted into a net/http server with `adaptor`. Reproduced on Fiber v2.51.0 and v2.52.15: three requests exported 0 spans and 0 histogram points.
- **Decision needed:**
  1. Finish buffered responses at handler return and keep the `Close` signal only for unknown-length streams (recommended). Fixes adaptor and Lambda and removes the leak for the common case. Duration then excludes the server-side write of a buffered body, which runs no user code.
  2. Keep the `Close`-only signal, document adaptor and Lambda as unsupported, and warn when no `Close` arrives. Still needs a separate answer to the registry leak.

### H2. Every handled 4xx error becomes an `exception` event on the SERVER span

Status: open
Class: b (needs decision)

- **Where:** `internal/server_errors.go:41-90`, called from `echo-v4/middleware.go:59`, `echo-v5/middleware.go:52`, `fiber-v2/middleware.go:99`, `fiber-v3/middleware.go:78`, `gin-v1/middleware.go:43`.
- **Problem:** `RequestState.CaptureError` calls `captureError`, which adds the span event immediately, before the final status is known. Echo and Fiber return router 404/405 errors through the middleware, so every unmatched request, every `echo.ErrUnauthorized`, every `fiber.Error` 4xx and every Gin `c.Bind` 400 gets an exception event. A 4xx error captured first also takes the first-error slot. The recorded-500 rule filters only server error counts, not the span event.
- **Reference:** spec.md section 6.4 requires the event for unhandled exceptions. JS records framework-channel errors only when the status is missing or >= 500; Python (Litestar skips `HTTPException` < 500) and .NET record only escaping exceptions.
- **Scenario:** bot scans against an Echo or Fiber app produce thousands of request logs, each showing an exception. Reproduced on echo-v4 (`GET /api/v1/missing` exported `exception{type: echo.HTTPError}`).
- **Fix:** store the returned error in request state when it arrives, and add the span event in `FinishObservation` only when the status is >= 500. The public `CaptureError` keeps adding its event immediately. Validation-detail extraction is unchanged.

## Medium

### M1. Span registry grows without limit during long-running requests

Status: open
Class: b (needs decision)

- **Where:** `internal/requests.go:311-316` (`link`), `:349-351` (`addLocked`); entries are removed only at release.
- **Problem:** `OnStart` links every descendant into `registry.entries` and `RequestState.members`. The 1,000-span cap limits buffered ended spans, not links. Python drops entries when their span ends.
- **Scenario:** SSE, long-poll or bulk-import endpoints that start a span per event or row. A scratch test with 100,000 child spans left 100,001 entries before release. 1,000 connections at one span per second for 8 hours is about 29M entries.
- **Fix:** in `link`, stop linking once `len(s.members)` reaches `maxBufferedSpansPerRequest`; spans past the cap are dropped anyway.

### M2. `NewSlogHandler(...).WithAttrs` passes application panics through

Status: fixed
Class: a (mechanical)
Note: Error values are converted with `fmt.Sprint`, which recovers `Error` panics as slog's handlers do. Covered in `TestCapturedValuesAreConvertedAndTruncated`.

- **Where:** `internal/slog_handler.go:70-73,235-236`.
- **Problem:** `WithAttrs` calls `ownedSlogAttrs` without `recover`, and that calls `value.Error()` directly. slog's own handlers recover from typed-nil `Error()` panics and print `<nil>`. This runs on every `logger.With(...)`, also when the SDK is inactive or `CaptureLogs` is false.
- **Scenario:** `logger.With("error", err)` with a typed-nil error whose `Error()` dereferences the receiver crashes the app only when wrapped by Apitally. Reproduced.
- **Fix:** convert attributes lazily in `capture`, which already recovers. This also removes the conversion cost from every `With` call when nothing is captured.

### M3. Gin response writer wrapper hides `Unwrap`

Status: fixed
Class: a (mechanical)
Note: Added `Unwrap` and `TestResponseControllerReachesUnderlyingWriter` (gin-v1).

- **Where:** `gin-v1/response_writer.go:12-31`.
- **Problem:** the wrapper embeds the `gin.ResponseWriter` interface, which has no `Unwrap`, although Gin's `*responseWriter` has it since v1.9.1. `http.NewResponseController(c.Writer)` returns `http.ErrNotSupported` for `SetWriteDeadline`, `SetReadDeadline` and `EnableFullDuplex`. design.md section 7 requires `Unwrap`.
- **Scenario:** an SSE handler on a server with `WriteTimeout` clears its write deadline; with Apitally the call fails and the stream is cut off. Reproduced.
- **Fix:** add `func (w *responseWriter) Unwrap() http.ResponseWriter { return w.observed }` and a Gin test.

### M4. Data race on the shared encoded resource when a log contains invalid UTF-8

Status: fixed
Class: a (mechanical)
Note: Only changed strings are written. The resource is not sanitized separately, because it would only contain invalid UTF-8 if `OTEL_RESOURCE_ATTRIBUTES` did.

- **Where:** `internal/otlp_encoding.go:190-208` (`replaceInvalidUTF8`), called from `internal/spool.go:70-75`; shared resource at `internal/runtime.go:150-155`.
- **Problem:** when marshalling fails, `replaceInvalidUTF8` calls `Set` on every string field of the whole message, including the `encodedResource` shared by the log batcher and the export worker, while the export worker may be marshalling metrics with the same resource. Reproduced under `-race`.
- **Scenario:** an app logs raw bytes (`slog.String("q", rawQuery)`) during an export cycle. The written values are identical, but `-race` builds in CI or staging report a race in Apitally.
- **Fix:** call `Set` only when `strings.ToValidUTF8` changed the string, and sanitize the resource once in `encodeResource`.

### M5. Per-request and per-span overhead on the hot path

Status: open
Class: b (needs decision)

- **Where:** `internal/requests.go:112-114,264`, `internal/span_processor.go:17-28`, `internal/requests.go:296-352`.
- **Problem:**
  - The default User-Agent exclusion regex costs about 8 us per request with a typical browser UA, plus about 2 us for the default path regex, uncached. Python memoizes both.
  - `BeginRequest` always builds an export copy (attribute copy, merged map, three span lock acquisitions), which only `SampleOnRequest` reads.
  - `OnStart`, `OnEnd` and log capture all take the single process-wide `requestRegistry.mu`, including spans unrelated to requests in attach mode.
- **Fix:** cache exclusion results in a small bounded map (or match the default UA fragments with `strings.Contains` on a lowercased UA); build the export copy only when `SampleOnRequest` is set; consider `sync.Map` (with `CompareAndDelete` for owner-checked removal) or sharding for the registry.

### M6. README documents behavior the code does not have

Status: fixed
Class: a (mechanical)
Note: Removed the Sentry claim, qualified the stack trace claim and listed `OTEL_SDK_DISABLED`.

- **Where:** `README.md:50-52`, `README.md:253`.
- **Problem:** the README promises errors "linked to Sentry issues automatically" (Sentry is deferred beyond v1 in design.md section 14), "stack traces for 500 error responses" (errors returned to Echo, Fiber and Gin have an empty stack trace), and omits `OTEL_SDK_DISABLED` from the disable controls.
- **Fix:** remove the Sentry claim, qualify the stack trace claim (panics and explicit `CaptureError`), and list `OTEL_SDK_DISABLED`.

### M7. Echo v4 routes registered on host routers get no route

Status: fixed
Class: a (mechanical)
Note: `listRoutes` includes the routers of `e.Routers()`, sorted by host. Added `TestHostRouterRequestHasRoute` (echo-v4).

- **Where:** `echo-v4/middleware.go:48,69`.
- **Problem:** the route allowlist and startup `paths` come from `e.Routes()`, which covers only the default router. Routes on `e.Host(name)` routers get route `""`, so no metrics, no errors, no `http.route`, and a span named `GET`. Echo v5 is not affected. Reproduced.
- **Fix:** build the allowlist and startup listing from the default router plus every router in `e.Routers()` (available at the v4.11.4 floor).

## Low

### L1. W3C propagator is registered globally in attach and private provider modes

Status: open
Class: b (needs decision)

- **Where:** `internal/providers.go:49-51`.
- **Problem:** the propagator is registered whenever the global propagator is unset, regardless of provider mode. The design.md section 16 table registers it only when the provider is also unset, and section 8 says the middleware extracts with W3C "without registering it". `implementation-plan.md:179` says "propagator registration only when unset", which conflicts.
- **Scenario:** an app that sets a `noop.TracerProvider` to disable tracing starts forwarding inbound `traceparent` and baggage headers through its `otelhttp` clients after activation.
- **Fix:** move the registration into the `global == initialTracerProvider` branch.

### L2. Fiber hook flush can block well past its 5 s deadline

Status: fixed
Class: a (mechanical)
Note: The cycle mutex is now a one-slot channel, so `Flush` stops waiting at its deadline and re-checks that Apitally is active after acquiring it.

- **Where:** `internal/export_worker.go:51-63`.
- **Problem:** `Flush` creates a 5 s context, then takes `r.cycleMu` without honoring it. A running export cycle can hold the lock for many sequential POSTs (10 s timeout each, 20 s with the inline retry). `Flush` also checks `active` before taking the lock, so a concurrent `Shutdown` can see spool files recreated after `deleteAll`.
- **Scenario:** slow or unreachable ingestion during a Kubernetes rollout makes `app.ShutdownWithContext` block in Fiber's shutdown hook for tens of seconds.
- **Fix:** acquire the cycle lock with a context-aware semaphore, and re-check `active` after acquiring it.

### L3. Shutdown with an expired deadline can leave orphaned spool files

Status: fixed
Class: a (mechanical)
Note: `deleteAll` marks the spool deleted, which turns later appends into no-ops.

- **Where:** `internal/runtime.go:185-196`.
- **Problem:** the batch span processor and log batcher return on context expiry while their goroutines keep draining; `spool.deleteAll()` runs, then a late `spool.append` creates a new temp file that is never sent and is only removed by a later process after 2 hours.
- **Fix:** set a closed flag in `deleteAll` that turns `append` into a no-op.

### L4. Spool writes are unbuffered and allocate a gzip writer per file

Status: fixed
Class: a (mechanical)
Note: Added a 64 KiB `bufio.Writer` below the gzip writer. gzip writers are not reused: a few allocations per export cycle do not justify pooling state.

- **Where:** `internal/spool.go:212-219`.
- **Problem:** `gzip.Writer` writes straight to the file; `compress/flate` flushes every 246 bytes, so a 4 MB file costs about 1,500 write syscalls under `spool.mu`. Each new file allocates about 1 MB in `gzip.NewWriter`.
- **Fix:** put a `bufio.Writer` (64 KiB) under the gzip writer and flush it on close; reuse gzip writers with `Reset`.

### L5. `Host` header is missing from captured request headers on Chi, Echo and Gin

Status: fixed
Class: a (mechanical)
Note: Covered in `TestCapturedHeadersAreRedacted`.

- **Where:** `internal/nethttp.go:104`, `internal/requests.go:146`.
- **Problem:** net/http removes `Host` from `r.Header`. Fiber includes it, as do Python, JS and .NET. spec.md section 6.1 captures all headers when the toggle is on.
- **Fix:** add `Host` from `r.Host` when capturing request headers.

### L6. Echo client address trusts client-supplied forwarding headers by default

Status: open
Class: b (needs decision)

- **Where:** `echo-v4/middleware.go:51`, `echo-v5/middleware.go:44`.
- **Problem:** without an `IPExtractor`, `c.RealIP()` uses the leftmost `X-Forwarded-For`, then `X-Real-IP`, from any client. spec.md section 6.1 says SDKs must not trust client-supplied forwarding headers. design.md section 8 chooses `c.RealIP()` without noting this.
- **Fix:** at minimum, tell Echo users in the README's trusted proxies section to set `IPExtractor`, and record the behavior in design.md.

### L7. Query parameter redaction skips names containing a malformed escape

Status: fixed
Class: a (mechanical)
Note: Added `decodeQueryParamName`. Covered in `TestQueryParametersAreRedactedOnAllExportedSpans`.

- **Where:** `internal/redaction.go:50-53`.
- **Problem:** when `url.QueryUnescape` fails, the raw name is matched, so `api%5Fkey%zz=secret` does not match `api[-_]?key` and the value is exported. spec.md section 6.7: "a malformed escape MUST NOT prevent redaction". Python's `unquote_plus` decodes valid escapes and keeps malformed ones.
- **Fix:** decode valid escapes and leave malformed ones in place before matching.

### L8. SDK and framework versions carry a `v` prefix

Status: fixed
Class: a (mechanical)
Note: `moduleVersion` strips the prefix for the SDK and framework versions.

- **Where:** `internal/resource.go:40-55`, used by `User-Agent` (`internal/export_client.go:64`), `telemetry.distro.version` and the startup event's `versions`.
- **Problem:** Go module versions are reported as recorded (`v1.0.0`); the other SDKs report bare semver, and the server displays `distro_version`.
- **Fix:** strip the leading `v`.

### L9. Code location attributes are emitted on logs without a location

Status: fixed
Class: a (mechanical)
Note: Covered in `TestRequestLogsAreLinkedToServerSpanAndForwarded`.

- **Where:** `internal/otlp_encoding.go:48-53`.
- **Problem:** `code.function.name`, `code.file.path` and `code.line.number` are always emitted, as `""` and `0` when the slog record's `PC` is 0. spec.md section 8 allows line numbers 1 to 65535; the other SDKs omit them when unavailable.
- **Fix:** emit them only when `PC` is non-zero.

### L10. Chi likely misses the `Content-Type` that net/http sets when sniffing

Status: open
Class: b (needs decision)

- **Where:** `internal/nethttp.go` (response header capture), pinned by `chi-v5/middleware_test.go` `TestRequestExportsSingleServerSpanWithStableSemconv`.
- **Problem:** the expected attribute map omits `http.response.header.content-type`, which Gin and Fiber include. net/http sniffs and sets `Content-Type` on first write, apparently after the wrapper read the headers. Python's `test_response_headers_include_headers_added_by_framework` requires it.
- **Fix:** verify; if confirmed, read response headers after the underlying write has committed them and update the test.

## Structure and naming

### S1. `RequestState.CaptureError` and `internal.CaptureError` mean different things

Status: fixed
Class: a (mechanical)
Note: Renamed to `CaptureReturnedError` and `returnedError`.

- **Where:** `internal/server_errors.go:41`, `internal/helpers.go:47`, `internal/requests.go:79-80`.
- **Problem:** the method records an error returned through the framework without a stack and feeds validation detection; the function is the public API that records the caller's stack. "Channel" in `channelError` reads as a Go `chan`.
- **Fix:** rename the method to `CaptureReturnedError` and the field to `returnedError`; update the six middleware call sites.

### S2. Exported identifiers in `internal` used only within `internal`

Status: fixed
Class: a (mechanical)
Note: Unexported all six; `HostFromAddress` was inlined.

- **Where:** `BeginRequest` (`requests.go:90`), `RequestState.FinishObservation` (`requests.go:121`), `RequestStateFromContext` (`requests.go:277`), `RequestState.CapturePanic` (`server_errors.go:55`), `NetHTTPRequestInfo` (`nethttp.go:93`), `HostFromAddress` (`server_span.go:121`).
- **Fix:** unexport them. `HostFromAddress` is a single-use wrapper over `splitHostPort`; inline it in `NetHTTPMiddleware`.

### S3. Dead code

Status: fixed
Class: a (mechanical)

- **Where:** `maxErrorConsumer` (`server_errors.go:27`) is unused; `validationDetail.source` (`validation_errors.go:20`) is never assigned.
- **Fix:** delete `maxErrorConsumer`; drop the `source` field and emit an empty `source` directly in `validationErrorEventBody` with a short comment that the validator does not report the request component.

### S4. `internal/helpers.go` is a grab bag

Status: fixed
Class: a (mechanical)

- **Problem:** it holds unrelated public helpers and a floating comment attached to no declaration; `SetConsumer` and `CaptureValidationError` have no doc comments.
- **Fix:** move `SetConsumer` to `consumers.go`, `CaptureError` to `server_errors.go`, `CaptureValidationError` to `validation_errors.go`, `SetRequestAttributes` to `requests.go`, and delete the file.

### S5. Misplaced types and files

Status: fixed
Class: a (mechanical)

- `payloadStash` (`body_capture.go:78-86`) carries headers as well as bodies and is used only by `requests.go` and `span_exporter.go`; move it to `span_exporter.go`.
- `gin-v1/response_writer.go` breaks the rule that framework packages contain only `middleware.go` and `sdk.go`; merge it into `middleware.go`.

### S6. Duplicated logic

Status: fixed
Class: a (mechanical)
Note: Added `internal.NewRouteSet` (Chi, Echo v4), a shared `bodyReader` in `body_capture.go`, and moved the nil check into `redaction.processBody`.

- The lazy route set (`sync.OnceValue` over `listRoutes`) is built identically in `chi-v5/middleware.go:34-41` and `echo-v4/middleware.go:29-36`; add one helper in `internal/startup.go`.
- `requestBody.Read` (`nethttp.go:118-130`) and `responseStream.Read` (`fasthttp.go:205-217`) are identical; share one counting and capturing reader in `body_capture.go`.
- `spanExporter.processBody` (`span_exporter.go:147-152`) only adds a nil check; move the check into `redaction.processBody` and call it directly.

### S7. Goroutines and public helpers without their own panic recovery

Status: fixed
Class: a (mechanical)

- `go r.logs.run()` (`log_batcher.go:103`) and `go r.runExportLoop(ctx)` (`export_worker.go:22`) rely on recovery in inner functions; AGENTS.md requires every goroutine to recover its own panics. Add `defer recoverAndLogPanic(...)` as the first statement.
- `SetRequestAttributes` (`helpers.go:25`) does not recover panics, unlike its sibling helpers.

### S8. Test-only code in production

Status: fixed
Class: a (mechanical)

- `setExportTransportForTest` (`export_client.go:106-112`) is called only from tests; move it to `export_client_test.go` and keep only the hook variable in production code.

### S9. Unclear names

Status: fixed
Class: a (mechanical)
Note: Renamed to `flushToSpool`, `requestRegistry.close`/`isClosed`, `newExportSpan`/`newRequestExportSpan`, `isProviderOwned`, `isActive`, `settings.isEnabled`, `spool.isInMemory` and `logRecord.record`.

- `flushIntake` (`export_worker.go:66`) moves all buffered telemetry into the spool; rename to `flushToSpool` and make the comment state why error events precede the log flush.
- `cutOff`/`isCutOff` (`requests.go:341-347`, `runtime.go:187`): rename to `close`/`isClosed`.
- `newExportSpanCopy` vs `newExportSpan` (`span_exporter.go:31,44`) differ only by "Copy"; rename to `newExportSpan(span, attrs)` and `(r).exportCopy(s, span)`.
- Booleans without the `is` prefix: `ownsProvider` (next to `isProviderPrivate`), `active`, `settings.enabled`, `spool.inMemory`.
- `logRecord.Record` (`log_batcher.go:24`) is exported on an unexported type; rename to `record`.

### S10. Declaration order

Status: fixed
Class: a (mechanical)

- Exported entry points come after unexported helpers in `requests.go:274-283` (`RequestStateKey`, `RequestStateFromContext`), `export_worker.go:51` (`Flush`), `nethttp.go:133` (`ResponseWriter`) and `fasthttp.go:130` (`HeaderFromValues`).

### S11. Smaller simplifications

Status: fixed
Class: a (mechanical)
Note: `isRegistered` was inlined as `lookup(...) == nil`.

- `runtime.go:120`: `r.activateOnce.Do(func() {})` prevents activation after `Shutdown`; add a one-line comment stating that.
- `fasthttp.go:165`: `bodyStreamFields sync.Map` keyed by type handles multiple `fasthttp.Response` types, which a binary cannot have; use a `sync.Once`.
- `fasthttp.go:51-53`: the `o.State == nil` check in `FinishHandler` duplicates the callers' check; keep the callers'.
- `requests.go:325`: inline the single-use `isRegistered` at `server_span.go:43`.

## Tests

### T1. Captured payloads in user exporters are untested in attach mode

Status: open
Class: b (needs decision)

- Add an attach-mode test with request and response body and header capture on, asserting the user's exporter receives none of `apitally.request.body`, `apitally.response.body` and `http.request.header.*` while Apitally's copy has them.

### T2. Sampling tests do not cover descendants, logs or errors

Status: open
Class: b (needs decision)

- Add `TestSampledOutRequestDropsDescendantsAndLogs` for request and response sampling.
- Add `TestSampleRateZeroDropsTraceButKeepsServerError`.
- Cover response-stage abstain (keeps earlier decision) and fail open on panic or invalid rate, including the warning.
- The abstain case in `TestSampleOnRequestFailsOpenAndAbstentionFallsBackToSampleRate` uses `SampleRate=0`, so drop-on-abstain also passes; add a `SampleRate=1` case expecting the request to be kept.

### T3. `SetRequestAttributes` is untested

Status: open
Class: b (needs decision)

- 0% coverage in every module. Add an internal test asserting the attributes on the SERVER export copy.

### T4. Shutdown contracts are untested

Status: open
Class: b (needs decision)

- A request in flight at `Shutdown` is discarded.
- `Shutdown` leaves a user-owned provider running (it still exports a new span afterward).

### T5. Error capture paths are untested

Status: open
Class: b (needs decision)

- Gin `c.AbortWithError(500, err)`.
- Fiber's `SendStatus(500)` fallback when the custom `ErrorHandler` fails.
- A panic after the response started records the committed status.
- The re-panicked value is unchanged (`recoverPanics` in `server_errors_test.go:20` discards it).
- An `http.ErrAbortHandler` panic is not captured.
- Excluded requests are not error-captured.

### T6. Websocket exclusion is untested

Status: open
Class: b (needs decision)

- Add a Gin test where a hijacking handler receives an `Upgrade: websocket` request: the raw response arrives unchanged and no span, metric point or error is exported. Add the case to `TestExcludedRequestsAreNotExportedOrSampled`.

### T7. Fiber stream contracts are untested

Status: open
Class: b (needs decision)

- Telemetry created inside `SetBodyStreamWriter` is exported with the request.
- Duration covers the stream write.
- The request is released once.
- An unknown-length stream over the 50,000-byte cap yields `[BODY_TOO_LARGE]`.
- `Close`/`CloseWithError` are forwarded to the original stream (`fasthttp.go:238,246,254` uncovered).

### T8. Log pipeline limits are untested

Status: open
Class: b (needs decision)

- The 1,000 log records per request cap.
- A log record emitted after release is dropped (only the span case exists).
- Release order: descendants, SERVER span, logs.

### T9. Body capture gaps

Status: open
Class: b (needs decision)

- A gzip body decompressing beyond the cap yields `[BODY_TOO_LARGE]` (`body_processing.go:28` uncovered).
- Decompression works with `CaptureResponseHeaders=false`.
- A chunked request read to EOF takes its size from the byte count (`nethttp.go:83` uncovered).
- `MaskResponseBody` is never exercised.

### T10. Startup, activation and writer gaps

Status: open
Class: b (needs decision)

- Echo v5 omission of `RouteAny` and `RouteNotFound` registrations in `TestStartupEventPathsMatchRoutes`.
- Startup `paths` is the union of all apps passed to `Init` before activation.
- Tracers obtained before activation nest under the request.
- `Unwrap` / `http.ResponseController` (`nethttp.go:213` uncovered); add a `ResponseController.Flush` call to the streaming test.
- `TestConsumersFromReusedRequestMemoryAreKept` exists only in fiber-v2; add it to fiber-v3 in the same position.

### T11. Duplicate coverage

Status: fixed
Class: a (mechanical)
Note: Applied as listed.

- Delete `TestRetriedFileIsSentByteIdentically` (`export_worker_test.go:19`); the 503 case of `TestRetryableFailureEndsCycleAndRejectedFileIsDropped` already compares bodies.
- Remove the server error event count assertion from `TestUnhandledPanicRecordedOnServerSpan` in fiber-v2, fiber-v3 and gin-v1; the internal panic test owns it, and the shared scenario differs across modules.
- Trim `TestRequestExportsServerSpanWithHandlerSpans` (`requests_test.go:22`) to the child's parent ID, kind and scope; framework tests own the attribute map.
- Remove the `http.response.body.size` check from `TestResponseWriterKeepsStreamingAndHijacking`; keep flush delivery and hijack assertions.

### T12. Tests of internal mechanisms

Status: open
Class: b (needs decision)

- Replace `TestHistogramGrowsBucketsInBothDirections` (private fields) and `TestHistogramBucketIndexMatchesOpenTelemetryMapping` with one test asserting the exported exponential histogram data point (scale, zero count, offset, bucket counts).
- `TestRequestLoggingMiddlewareIsRecognizedByFunctionName` restates the prefix list and never runs the PC resolution (`slog_handler.go:101`); delete it or replace it with a test logging from a function in a matching package path.
- Drop the `rotateForExport()` return value assertion in `TestSpoolRotatesFilesBeforeExceedingMaxUncompressedSize`.
- `TestMetricCombinationsAreCappedPerIntervalAndSplitIntoRequests` takes about 10 s under `-race`; check whether the split needs 50,001 combinations.

### T13. Test rule violations

Status: fixed
Class: a (mechanical)
Note: Applied as listed. The group prefix scenario now sends the group-root request in gin-v1, echo-v4 and echo-v5 (each gained a group-root route), matching chi and Fiber.

- `require.NoError` inside the `/raw` handler goroutine in `nethttp_test.go`; use `assert` or report through a channel.
- Indexing without `require.Len`: `server.Spans(t)[0]` in `TestReadFromUsesWrappedWriterUnlessBodyIsCaptured`, `userSpans.GetSpans()[0]` in `providers_test.go:41`.
- Inexact assertions: `tc.isExported == (len==1)` in `TestSampleOnRequestFailsOpen...`, `NotEmpty` in `TestExportRequestsCarry...Headers` (assert exactly 2), SERVER-only count in `TestStackedServerSpanInsideRequestIsExportedAsInternalWithWarning` (assert both inner spans are INTERNAL and the warning text), no total span count in `TestResponseWriterKeepsStreamingAndHijacking`, `assert.Positive` on the line number in `TestRequestLogsAreLinkedToServerSpanAndForwarded`.
- Duplicate subtest name `gzip` in `TestCompressedResponseBodiesAreDecompressedBeforeRedaction`; name subtests by behavior.
- `TestActivationWarnsWhenNoSlogHandlerWasCreated` tests `runtime.go`; move it to `runtime_test.go`.
- gin-v1 `TestRouteIncludesGroupPrefix` omits the group-root (`/api/v1`) request the other modules send; align the shared scenario.

### T14. Test helpers to consolidate in `internal/testutils`

Status: fixed
Class: a (mechanical)
Note: Added `testutils.Serve`, `OTLPServer.ApplicationLogRecords` and `OTLPServer.DecodeStartupEvent`.

- `serve` (identical in four net/http modules) and internal `httptestServer` into `testutils.Serve`.
- Application log filtering by `Scope == "slog"` (three internal tests, all six modules) into an `OTLPServer` method.
- Startup event JSON decoding into a helper.
- Delete `testWriteToken` (`config_test.go:14`), which duplicates `testutils.WriteToken`.

### T15. Flakiness risks

Status: fixed
Class: a (mechanical)
Note: The hijack test waits until the middleware returned; the interval test asserts the jitter range; the Fiber aborted-stream tests block until the client disconnected instead of writing 100 MB.

- `TestResponseWriterKeepsStreamingAndHijacking`: `Shutdown` can discard the `/raw` request because the client finishes before the handler returns; wait for the handler before asserting an exact span count.
- `TestExportIntervalHeaderIsClampedToRange`: the `InDelta` tolerance equals the jitter bound; assert the range [0.9, 1.1] x interval explicitly.
- `TestAbortedStreamOmitsSize` (Fiber) writes 100 MB to force a write error; use a writer that blocks until the client disconnects.

## Repository and CI

### R1. `.DS_Store` is tracked

Status: fixed
Class: a (mechanical)

- Remove it and add `.DS_Store` to `.gitignore`.

### R2. POCs and design review documents are committed

Status: open
Class: b (needs decision)

- `pocs/` (five modules with results and logs), `docs/design-review.md`, `docs/design-review-2.md` and `docs/design-review-3.md` are tracked. Remove them before v1 replaces `main`; git history keeps them.

### R3. CI does not run `make check`

Status: fixed
Class: a (mechanical)

- `.github/workflows/tests.yaml` runs only `go test -race`, so `gofmt`, `go vet`, `go mod verify` and `go mod tidy -diff` are not enforced. Add a check job, at least on the floor toolchain.

### R4. Stray test binary in the repository root

Status: fixed
Class: a (mechanical)

- `internal.test` (18 MB) is ignored by `*.test` but should be deleted.
