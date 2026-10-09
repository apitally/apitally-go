# v1 code review, round 2

Review of the `v1` branch at commit `02f8b13`. Four reviewers each covered one focus:
- re-review of every fix since the first implementation;
- the subsystems that no earlier review examined;
- route matching and fasthttp memory reuse;
- lifecycle, concurrency and panic safety.

Every finding was confirmed with a failing test before it was reported. Before the reproduction tests were added, `go test -race -count=20` passed in all seven modules.

Each finding has an ID, a status (`open`, `fixed`, `rejected` or `deferred`) and a class:
- **Class a:** a mechanical fix. It is minimal, unambiguous and safe to apply without a decision.
- **Class b:** needs a decision.

Locations refer to commit `02f8b13`. The reproduction tests are named `review2_*_test.go` and live on these branches:
- `pi-agent-446d7685-2610-4f2`: F findings.
- `pi-agent-335370b9-2750-418`: U findings.
- `pi-agent-a51bef85-cdd0-451`: C findings.
- `pi-agent-2f9b4ffd-9fd1-454`: R findings.

Update the status as findings are addressed, and add a short note for anything rejected or deferred.

## Medium

### C3. Captured slog values ignore `String()` methods with pointer receivers

Status: fixed
Class: a (mechanical)
Note: Errors and `fmt.Stringer` values are formatted with `fmt.Sprint`. Covered in `TestCapturedValuesAreConvertedAndTruncated`.

- **Where:** `internal/slog_handler.go:255-259`, the `reflect.Pointer` case in `ownedAnyValue`.
- **Problem:** the pointer is dereferenced before formatting, so a `String()` method declared on the pointer type never runs. `%+v` then prints every field, including unexported ones. slog's `TextHandler` calls `String()`, so the application's own logs hide the fields while Apitally exports them.
- **Scenario:** `logger.InfoContext(ctx, "loaded", "patient", &p)`, where `(*Patient).String` deliberately omits the name.
  - The application log shows `patient="patient p-1"`.
  - Apitally exports `{ID:p-1 Name:Jane Doe}`.
  - This sends data the application deliberately keeps out of its logs to a third party.
- **Reproduction:** `internal/review2_slog_handler_test.go`, `TestReview2CapturedPointerValueUsesItsStringMethod`.
- **Fix:** check for `fmt.Stringer` before the `reflect` switch and return `fmt.Sprint(value)`. `fmt` recovers panics from typed-nil receivers. About 3 lines.

### U1. A gzip request body is exported compressed and unredacted when an inner middleware decompresses it

Status: fixed
Class: a (mechanical)
Note: `RequestState` keeps the `Content-Encoding` read when the request begins. `TestCompressedResponseBodiesAreDecompressed` became `TestCompressedBodiesAreDecompressed`, which also sends each body as a request through a handler that removes the header, as decompression middleware does. This also adds the first coverage of request decompression.

- **Where:** `internal/requests.go:186` reads `Content-Encoding` from the live request header map (`internal/nethttp.go:200`) when the request finishes.
- **Problem:** a decompressing middleware registered after `Init` deletes `Content-Encoding`. Apitally has already captured the compressed bytes, so it skips decompression and JSON redaction fails. The raw gzip bytes are exported, and `MaskRequestBody` also receives compressed bytes.
- **Scenario:** Gin with `gzip.Gzip(..., gzip.WithDecompressFn(gzip.DefaultDecompressHandle))` from gin-contrib/gzip, with `CaptureRequestBody` on. A body like `{"password": ...}` is exported unredacted, in gzip form.
- **Reproduction:** `internal/review2_body_processing_test.go`, `TestReview2GzipRequestBodyIsRedactedWhenInnerMiddlewareDecompresses`.
- **Reference:** .NET reads `Content-Encoding` when the request starts, for this reason (`RequestState.cs:294`).
- **Fix:** copy the request `Content-Encoding` when the request begins and use the copy at finish. About 3 lines.

### C1. `Shutdown` overruns its deadline while a Fiber shutdown hook is flushing

Status: fixed
Class: a (mechanical)
Note: `Flush` derives its deadline from the export context, which `Shutdown` cancels first. Taking the cycle lock needs no deadline, because every holder now stops when `Shutdown` begins. Covered by a concurrent `Flush` in `TestShutdownHonorsContextDeadline`.

- **Where:**
  - `internal/runtime.go:188` takes `cycleLock` unconditionally.
  - `internal/export_worker.go:29`: `Flush` builds its 5 s timeout from `context.Background()`.
- **Problem:** `Shutdown` cannot cancel a running hook `Flush`. With a slow backend it waits up to 5 s, whatever its own deadline, then runs the final drain with an expired context, so nothing more is delivered.
- **Scenario:** Fiber v3 `app.Listen(addr, fiber.ListenConfig{GracefulContext: ctx})`, or Fiber v2's documented graceful recipe.
  - `Listen` returns before Fiber's shutdown hooks run.
  - So `main` calls `apitally.Shutdown(ctx)` while the hook's `Flush` is still in progress.
- **Reproduction:** `internal/review2_shutdown_test.go`, `TestReview2ShutdownDuringHookFlushHonorsDeadline`. With a 1 s deadline, `Shutdown` took 5 s.
- **Fix:**
  - Derive `Flush`'s context from a runtime context that `Shutdown` cancels.
  - Take `cycleLock` in a `select` that also watches `ctx.Done()`. On expiry, skip the drain but still do the cleanup.
  - About 5 lines.

### R1. Fiber v3: a request that falls through to a trailing `Use` handler is recorded under route `/`

Status: fixed
Class: a (mechanical)
Note: Uses Fiber v2's route lookup by first handler. The suggested `Method != "USE"` check does not work, because Fiber v3's `addRoute` sets each `Use` route copy's method to the request method. `TestUnmatchedRequestHasNoRouteAndNoHistogramPoint` (fiber-v3) adds a route that passes a missing asset on to a not-found `Use` handler. Passes on v3.0.0 and v3.5.0.

- **Where:** `fiber-v3/middleware.go:66-67`.
- **Problem:** Fiber v3 keeps `Matched()` true after a route calls `c.Next()`. `c.Route()` then returns the `Use` route that handles the request, with path `/`.
- **Scenario:** `app.Get("/assets/*", static.New(dir))` followed by `app.Use(notFound)`. The static middleware calls `c.Next()` for missing files.
  - Every missing asset becomes a 404 on route `/`.
  - If the app has `GET /`, those 404s are mixed into that endpoint's metrics. Otherwise the route is missing from the startup list.
- **Reproduction:** `fiber-v3/review2_routes_test.go`, `TestRouteFallingThroughToUseHandlerIsNotAttributedToUseRoute`.
- **Fix:** use Fiber v2's lookup, keyed by method and `&route.Handlers[0]` and built from `GetRoutes(true)`. About 15 lines.

### R2. Chi: routes under a router mounted at `/` inside another mounted router get no route

Status: fixed
Class: a (mechanical)
Note: `listRoutes` repeats the wildcard removal. The shared Chi test router now mounts `/users/{userID}` at the root of `/api/v1`, so `TestRouteIncludesGroupPrefix` and `TestStartupEventPathsMatchRoutes` cover it. Passes on Chi v5.1.0 and v5.3.2.

- **Where:** `chi-v5/middleware.go:49-51`.
- **Problem:** `listRoutes` replaces `/*/` once, while Chi's `RoutePattern` repeats the replacement until none is left. They disagree when two mount wildcards are adjacent. The startup list contains `/api/*/users`, requests report `/api/users`, and the route set rejects them.
- **Scenario:** `r.Mount("/api", api)` with `api.Mount("/", users)` or `api.Route("/", ...)`. The whole subtree loses its route, metrics and errors.
- **Reproduction:** `chi-v5/review2_routes_test.go`, `TestRouteOfSubrouterMountedAtRootOfMountedRouterIsListed`.
- **Fix:** repeat the replacement while the pattern still contains `/*/`, as Chi's `replaceWildcards` does. About 3 lines.

### F1. Fiber streams of unknown length never complete on adaptor and Lambda

Status: fixed
Class: b (needs decision)
Note: Decided option 1, refined: a wrapped stream completes at its first read error, including EOF, or at `Close`, whichever comes first. Every reader of a stream, including `Response.Body` in the adaptor and Lambda proxies, stops at its first error, so a stream that fails mid-read also completes. `TestRequestServedThroughAdaptorIsExported` (fiber-v2 and fiber-v3) also requests the unknown-length stream. Design sections 6 and 7 updated.

- **Where:** `internal/fasthttp.go:97`. A wrapped stream completes only on `Close`.
- **Problem:** this is the case H1 left open.
  - The adaptor and the awslabs Lambda proxy read the stream to EOF, but never reset the `RequestCtx`, so `Close` never arrives.
  - Fiber v2 never records such requests. Each one keeps its registry entries, request state, buffered spans and logs, and captured payloads until `Shutdown`.
  - Fiber v3 completes the request only when the pooled context is reused, so the duration includes idle time and the last request is lost.
- **Scenario:** a Fiber app on AWS Lambda serving `c.SendStream(body)` without a size, a CSV export through `SetBodyStreamWriter`, or SSE.
- **Reproduction:** `TestReview2UnknownLengthStreamServedThroughAdaptorIsExported` in fiber-v2 and fiber-v3. Of 3 requests, fiber-v2 exported 0 spans and fiber-v3 exported 1.
- **Decision needed:**
  1. Complete at EOF or `Close`, whichever comes first. `bodyReader` already sees EOF, and an aborted stream still completes on `Close`. About 10 lines plus design section 7. Recommended: it closes the leak and matches the other SDKs, where completion always fires.
  2. Document these streams as unsupported on adaptor and Lambda. The leak stays.

## Low

### C2. Data race on the request body wrapper when the body is read after the handler returns

Status: fixed
Class: a (mechanical)
Note: `bodyReader` guards its counters with a mutex, which `Finish` holds while reading them. No test added: a reproduction needs a reverse proxy with an early-responding raw TCP backend, is timing-dependent and fails only under `-race`. The reviewer's reproduction passed three runs with the fix.

- **Where:**
  - The write is at `internal/body_capture.go:75-80` (`bodyReader.Read`).
  - The read is at `internal/nethttp.go:83-86` (`Finish`).
  - The wrapper is installed for every chunked body.
- **Problem:** `httputil.ReverseProxy`'s transport goroutine can keep reading `req.Body` after `ServeHTTP` returns. `Finish` reads the wrapper's counters at the same time. The race detector flags it, and the recorded body size can be wrong.
- **Scenario:** an API gateway on Chi, Echo or Gin forwards a chunked upload, and the backend rejects it early (401 or 413).
- **Reproduction:** `internal/review2_nethttp_test.go` on the C branch, `TestReview2ReverseProxyRequestBodyReadAfterHandlerReturn`. It reports `DATA RACE`.
- **Fix:** guard `size`, `isEOF` and `capture` with a mutex, and have `Finish` take a snapshot under it. About 15 lines.

### F3. `io.Copy` responses on net/http record no detected Content-Type and no body

Status: fixed
Class: a (mechanical)
Note: `ReadFrom` first writes up to 512 bytes through `Write`, as net/http does. `TestDetectedContentTypeIsRecorded` adds an `io.Copy` route, and `TestReadFromUsesWrappedWriterUnlessBodyIsCaptured` now copies 1,000 bytes, so forwarding still happens after the first 512. Design section 7 updated.

- **Where:** `internal/nethttp.go:151-152`. `ReadFrom` calls `startBody(nil)`.
- **Problem:** this gap is left over from L10. net/http's own `ReadFrom` sniffs the first 512 bytes, so the client receives `text/plain; charset=utf-8`. Apitally records no Content-Type, so the body is not captured.
- **Scenario:** a Chi handler does `io.Copy(w, upstream.Body)` without setting a Content-Type. Gin and Echo writers have no `ReadFrom`.
- **Reproduction:** `internal/review2_nethttp_test.go` on the F branch, `TestReview2ReadFromRecordsDetectedContentType`.
- **Fix:** when the type is unset, read up to 512 bytes, pass them through `Write`, then delegate the rest, as net/http does. About 8 lines.

### U2. A JSON body that starts with a UTF-8 byte order mark is exported unredacted

Status: fixed
Class: a (mechanical)
Note: `redactJSON` strips a leading byte order mark. Covered by prefixing the input of `TestNestedJSONBodyFieldsAreRedacted`.

- **Where:** `internal/body_processing.go:46`, `redactJSON`.
- **Problem:** `json.Decoder` rejects the byte order mark. The body is valid UTF-8, so it is exported verbatim.
- **Scenario:** Windows PowerShell 5.1 or a .NET client writing with `Encoding.UTF8` sends `\xEF\xBB\xBF{"password":"..."}`.
- **Reproduction:** `internal/review2_body_processing_test.go`, `TestReview2JSONBodyWithByteOrderMarkIsRedacted`.
- **Reference:** Python (`json.loads` on bytes) and JS (`TextDecoder`) strip the byte order mark and redact.
- **Fix:** strip a leading byte order mark before `redactJSON`. One line.

### R4. Chi: HEAD requests served through `middleware.GetHead` get no route

Status: fixed
Class: a (mechanical)
Note: The route lookup uses `rctx.RouteMethod`. Covered by a HEAD request through `middleware.GetHead` in `TestRouteIncludesGroupPrefix`.

- **Where:** `chi-v5/middleware.go:38`.
- **Problem:** the route-set lookup uses `r.Method` (HEAD), but `GetHead` routes the request to the GET route. Fiber v2 records such requests with the GET route's path.
- **Reproduction:** `chi-v5/review2_routes_test.go`, `TestHeadRequestServedByGetRouteHasRoute`.
- **Fix:** look up `rctx.RouteMethod`, which equals `r.Method` unless `GetHead` changed it. About 3 lines.

### F2. Spans and logs from a stream writer are dropped when the response declares a Content-Length

Status: fixed
Class: b (needs decision)
Note: Decided option 1: design section 6 now states the limitation. A stream of known size whose writer runs user code that logs or creates spans is rare, because generated content rarely has a size known before it is written, and wrapping sized streams would slow every sized download.

- **Where:** `internal/fasthttp.go:80,97`.
- **Problem:** with a known size, the stream isn't wrapped and observation completes at handler return. That is before fasthttp runs the writer, so telemetry produced inside the writer arrives after release and is dropped. Design section 6 says it "remains eligible".
- **Scenario:** `SetBodyStreamWriter`, or `SendStream(pipe, size)`, with an explicit `Content-Length`. For example, a file export of known size that starts spans or logs while writing.
- **Reproduction:** `TestReview2StreamWriterSpanWithContentLengthIsExported` in fiber-v2 and fiber-v3.
- **Decision needed:**
  1. Correct design section 6 to state the limitation. Zero code. Recommended: the case is narrow, and both code options trade away something that matters more.
  2. Wrap every stream whose size comes only from the header, keeping `*os.File` unwrapped. About 5 lines. These streams lose the zero-copy path, and on adaptor and Lambda they depend on F1.

### F4. Content-Type detection sniffs only the first write

Status: open
Class: b (needs decision)

- **Where:** `internal/nethttp.go:220`.
- **Problem:** net/http sniffs all output buffered before the first flush, up to 2,048 bytes. Apitally sniffs only the first `Write` chunk. A short first chunk can produce a different type, which also changes whether the body is captured.
- **Scenario:** `html/template` writes `<html` before an `{{if}}` action.
  - Apitally records `text/plain; charset=utf-8`, while the client gets `text/html`.
  - The HTML body is captured, because `text/plain` is on the capture allowlist.
- **Reproduction:** `internal/review2_nethttp_test.go` on the F branch, `TestReview2SmallFirstWriteRecordsSentContentType`.
- **Decision needed:**
  1. Accept the gap. It only affects responses without an explicit Content-Type whose first write is shorter than the sniffed signature. JSON APIs, the main use case, set Content-Type or start with `{` or `[`. Recommended.
  2. Defer the sniff and capture decision until 512 bytes are written, a `Flush`, or `Finish`, holding the first bytes in the capture buffer. About 20 lines.

### R3. Chi: a router wrapped in middleware before `Mount` gets no route

Status: open
Class: b (needs decision)

- **Where:** `chi-v5/middleware.go:38,49`.
- **Problem:** `chi.Walk` cannot see through an `http.Handler` wrapper. It lists only `/admin/*`, while requests report the inner router's full pattern, which the route set rejects.
- **Scenario:** `r.Mount("/admin", middleware.BasicAuth(...)(adminRouter))`.
- **Reproduction:** `chi-v5/review2_routes_test.go`, `TestRouteOfRouterWrappedInMiddlewareBeforeMountIsListed`.
- **Decision needed:**
  1. Record it as a known limitation in design section 9. The walkable alternatives `adminRouter.Use(...)` and `r.With(...).Mount(...)` both work. Recommended.
  2. Accept patterns that extend a listed non-Chi mount wildcard. About 10 lines, and it reports routes that are absent from the startup list.

## Rejected

### U3. NDJSON bodies are never field-redacted

Status: rejected
Class: b (needs decision)
Note: Python, JS and .NET share this gap. Fixing it would need a cross-SDK spec change for a niche content type.

- **Where:** `internal/body_processing.go:46-53`.
- **Problem:** `redactJSON` requires a single JSON value, so a multi-line `application/x-ndjson` body is exported verbatim.

## Unconfirmed

Not reproduced, or not a defect in this repo. Listed so later rounds don't repeat the investigation.

- **Echo v5 `Any` routes:** `routePath` records them, but `listRoutes` omits them as spec section 9.1 requires. Whether the server keeps metrics for a route missing from the startup list is a question for the Cloud side.
- **`SetRequestAttributes` on Fiber:** strings nested inside `attribute.Map` or `attribute.Slice` values are not cloned. This would keep reused fasthttp memory, but usage is rare.
- **Attach mode:** an application calling `tp.Shutdown` at the same time as `apitally.Shutdown` makes `UnregisterSpanProcessor` wait on the provider's lock beyond Apitally's deadline.
- **Lost consumer updates:** `consumerUpdates.isChanged` records the payload hash before the event is queued. If the log queue is full, the update is dropped and not re-sent until the payload changes. Python has the same order.
- **Container CPU:** CPU utilization is divided by `runtime.NumCPU()`, which ignores cgroup limits. Python and JS behave the same; .NET differs. Design section 11 records this choice.

## Documentation

### D1. `docs/implementation-plan.md` describes the Gin middleware order from before `3784e27`

Status: fixed
Class: a (mechanical)
Note: Updated.

- **Where:** `docs/implementation-plan.md:135`. It still describes prepending the observer plus a separate panic-capture handler.
- **Fix:** describe the current behavior: `Init` appends the middleware like the other frameworks.
