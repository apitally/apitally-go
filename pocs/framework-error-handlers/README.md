# Framework error-handler POC

## Question

Can middleware registered first observe the first error/panic and the final response status, recognize validator errors without importing validator, and preserve the client's response? This tests design sections 8, 13 and 17, not an SDK implementation.

## Versions and running

Verified every framework below with `go list -m -json <module>@latest`:

| Dependency | Version tested |
| --- | --- |
| Gin | v1.12.0 |
| Echo v4 / v5 | v4.16.0 / v5.4.0 |
| Fiber v2 / v3 | v2.52.15 / v3.5.0 |
| Chi v5 | v5.3.2 |
| go-playground/validator v10 | v10.30.4 |
| fasthttp (shared by both Fiber versions through MVS) | v1.73.0 |
| Go, darwin/arm64 | 1.27.1 and 1.25.0 |

Validator's latest v10.30.5 requires Go 1.26; v10.30.4 is the latest compatible with Go 1.25. The module uses the canonical `go 1.25.0` directive required by these dependencies. All other resolved versions are pinned in `go.mod`/`go.sum`.

```sh
cd pocs/framework-error-handlers
go test -race -count=1 -v ./...
GOTOOLCHAIN=go1.25.0 go test -race -count=1 ./...
```

Both commands pass all seven packages. Tests drive real framework apps using `httptest` or Fiber `app.Test`; Chi recovery tests use actual HTTP servers. Below, **FAIL means a disproved design assumption**, not a failing test. Baseline comparisons run the same app with and without observation.

## Returned errors and error-handler ownership

| Framework / case | Result and evidence |
| --- | --- |
| Echo v4, Echo v5: HTTP errors 400/404/422, unmatched 404, plain error 500 | PASS with manual dispatch and return nil: observed status equals client status, identical body, one handler invocation and one body write. `TestReturnedErrorsAndRequestLoggerPrecedent` |
| Echo v4, Echo v5: guarded or unguarded custom handler returning 409 | PASS with return nil: identical custom status/body, exactly one invocation/write. Same test. |
| Echo v4, Echo v5: return original error after dispatch | FAIL exactly-once handling: outer middleware receives the error, but Echo invokes the handler twice. Default/guarded handlers write once; the unguarded custom handler appends its body twice. Same test. |
| Echo v4, Echo v5: actual RequestLogger with HandleError | Same failure as returning the original error: two handler invocations and duplicate body with an unguarded custom handler. Same test. |
| Fiber v2, Fiber v3: errors 400/422/500, plain error 500, wrapped 422 | PASS with app ErrorHandler and return nil: identical baseline status/body, one invocation, observed status is final. `TestReturnedErrorsMatchBaseline` |
| Fiber v2, Fiber v3: custom handler returning 409 | PASS: identical baseline status/body, one invocation. Same test. |
| Fiber v2, Fiber v3: error handler itself fails | PASS when reproducing the router's SendStatus(500) fallback: identical `Internal Server Error` body. `TestFailedErrorHandlerMatchesRouterFallback` |
| Fiber v2, Fiber v3: return original error after dispatch | FAIL: two invocations. Observer sees 421/`call 1`; client gets 422/`call 2` from the second dispatch. `TestReturnOriginalDuplicatesDispatch` |
| Fiber v2, Fiber v3: actual logger outside observation | Returning nil changes logged error from `logger error` to `-`, while status stays 500 and response is unchanged. `TestOuterLoggerLosesHandledError` |
| Gin: c.Error, AbortWithError(500), multiple errors | PASS: first c.Errors entry and final status 500 available after c.Next; ordinary captured errors have no stack. `TestErrorChannel` |
| Chi: ordinary HTTP 500, with/without explicit recording | PASS: status visible; only explicitly recorded errors are captured, first wins. `TestRequestContextAndExplicitError` |

Dispatch APIs are `c.Error(err)` on Echo v4, `c.Echo().HTTPErrorHandler(c, err)` on Echo v5, and `app.ErrorHandler(c, err)` on both Fiber versions. Echo v5 has no `c.Error` equivalent method.

**Decision:** return nil after handling an error to guarantee one dispatch. Fiber's logger follows this precedent. Echo's RequestLogger invokes the handler and returns the original error, relying on committed-response guards; that precedent does not satisfy the stronger exactly-once/custom-handler requirement. Returning nil hides the original error from outside middleware, including logging/instrumentation. A single ordinary middleware cannot offer both error propagation and exactly-once dispatch without additional coordination.

## Panics and final-status timing

| Framework / recovery position | Capture/re-panic and unchanged response | Final status at observer unwind |
| --- | --- | --- |
| Echo v4, Echo v5: Recover outside | PASS: original string/error/pointer survives; baseline body/status unchanged; error handler runs once | FAIL: observer sees 200 before default 500 or custom 503; a committed partial 202 remains 202 |
| Fiber v2, Fiber v3: Recover outside | PASS: original string/error/pointer survives; baseline body/status unchanged; error handler runs once | FAIL: observer sees handler's provisional 418 before default 500 or custom 503 |
| Gin: gin.Default Recovery outside | PASS: original value survives; same baseline response | FAIL: observer sees 200 before 500; custom recovery produces 418; committed partial 202 remains 202 |
| Chi: Recoverer outside | PASS: original error survives; same 500 response | FAIL: observer sees unwritten/default 200 before Recoverer writes 500 |
| Chi: Recoverer inside | FAIL original-panic capture: recovery consumes it before observer can recover; response unchanged | PASS: observer sees final 500 |
| Chi: no Recoverer, net/http server recovery | PASS identity and baseline behavior | FAIL HTTP-500 assumption: client receives EOF, no HTTP response; server logs ordinary panic |

Evidence: Echo `TestPanicWithOuterRecover`, Fiber `TestPanicCaptureAndOriginalRepanic`, Gin `TestOuterRecovery`, Chi `TestRecoveryOrderAndServerBaseline`. Provisional 200 above is not an emitted status on aborted requests.

`http.ErrAbortHandler` is excluded from capture and re-panicked unchanged on every integration. Actual net/http server tests show EOF and an empty server log, with and without observation. Chi and Echo Recover re-panic the sentinel; Gin Recovery consumes it without writing an error response; Fiber Recover turns it into an error response. The observer preserves each framework's baseline behavior. Wrapped cancellation is also excluded. Evidence: Echo `TestIgnoredPanicsPreserveIdentity` and the tests above.

**Conclusion:** recover/capture/re-panic preserves behavior and original panic identity, but cannot know a response that an outer recovery has not written yet. Moving recovery inside solves timing but loses the original panic/stack. Forcing status 500 is incorrect for partial responses, custom recovery and aborted connections.

## Validation recognition

The recognizing code is `observe/observe.go` and imports no validator. It walks `errors.Unwrap`, reflects only to identify/index a nonempty slice, and asserts each element against four methods: `Namespace() string`, `Field() string`, `Tag() string`, `Error() string`. `errors.As` to that interface does not work: the elements implement it, the slice does not. `TestValidationRecognition` proves this and direct/wrapped recognition.

| Case | Result and evidence |
| --- | --- |
| Direct validator.ValidationErrors and fmt.Errorf("%w") | PASS. Namespace `Input.Address.City`; registered JSON tag function gives `Input.address.city`. `observe/TestValidationRecognition` |
| Gin BindJSON | PASS: c.Errors contains real validator errors, status 400, Namespace `Input.Name`. ShouldBindJSON plus a handwritten 400 is intentionally invisible. `TestBindingVisibility` |
| Echo v4, Echo v5 c.Validate | PASS: direct validator errors recognized; default response is 500, custom mapping gives 400/422 with unchanged bodies. JSON-tag paths include `validationInput.email` and `validationInput.address.city`. `TestValidationReturnedDirectly` |
| Fiber v2 returned wrapped validator errors | PASS: recognized with custom 400/422/500 mapping, unchanged response, Namespace `input.email`. There is no built-in struct-validator setup in this test. `TestReturnedValidationErrors` |
| Fiber v3 c.Bind().JSON manual/auto and Body auto, struct validator | PASS recognition; default validation response is 500. Custom 400 and wrapped custom 422 retain validation details. JSON paths `validationRequest.email` / `validationRequest.address.city`. `TestBindValidationRecognitionAndStatus` |
| Fiber v3 c.Bind().All manual | PASS recognition, default status 500. Same test. |
| Fiber v3 c.Bind().WithAutoHandling().All | FAIL recognition: converts validation error to a 400 *fiber.Error without Unwrap; original structured details are unavailable. Same test. |
| Fiber v3 parsing error / valid input | PASS exclusion: parsing errors are not validation errors; valid input returns 200 without captured error. Same test. |

Recognition alone is not eligibility: automatic validation capture requires final 400/422, and server error counting requires a captured error plus final 500. Default Echo/Fiber validation responses can therefore count as server errors rather than validation errors. Strip only the leading struct-name component when constructing the design's `field`; preserve the remaining namespace.

## Deterministic stacks and context

PASS: `observe/TestPanicStackAndIdentity` captures panicking frames inside the deferred recover, checks the `function\n\tfile:line` format and compares two complete stacks from the same call/panic line byte-for-byte. `runtime.Callers(2, ...)` skips Callers and the observer's deferred Recover method; the remaining leading `runtime.gopanic` is explicitly removed. The first displayed frame is `panicLeaf`, not the middleware. Ordinary returned errors have an empty stack. The prototype trims leading capture/runtime frames; production must also omit remaining SDK middleware frames as section 8 requires. This is stability for the same call path/build, not an assertion that different callers or builds have identical stacks.

Every integration preserves a per-request state pointer through the following context APIs:

| Integration | Storage / handler lookup | Evidence |
| --- | --- | --- |
| Gin | replace c.Request using WithContext / c.Request.Context() | `TestContextFallback`: direct lookup through *gin.Context fails with fallback off, succeeds with it on |
| Echo v4, Echo v5 | c.SetRequest(...WithContext(...)) / c.Request().Context() | `TestRequestContextFirstErrorAndCancellation` |
| Fiber v2 | c.SetUserContext / c.UserContext | `TestUserContextStateAndFirstError` |
| Fiber v3 | c.SetContext / c.Context | `TestContextStateAndFirstError` |
| Chi | r.WithContext / r.Context() | `TestRequestContextAndExplicitError` |

The framework-specific Gin helper must use `c.Request.Context()` rather than pass `c` to the root helper. First-error retention and cancellation filtering pass; ordinary error capture leaves the stack empty.

## Proposed design changes (not applied)

1. Section 8: specify manual Echo/Fiber error dispatch followed by return nil, including Fiber's handler-failure fallback. Document outside-middlewares' loss of returned-error visibility. Do not adopt Echo RequestLogger's return-original behavior without an explicit committed-handler contract or dispatch coordination.
2. Sections 6/8/17: remove the blanket statement that a panicking request has final status 500. Separate original-panic capture from completion after recovery. The current single middleware does not satisfy both requirements; a follow-up design/POC must coordinate recovery and transport finalization. Aborted requests have no final HTTP response; partial/custom recovery statuses must remain unchanged.
3. Section 8 validation: qualify Fiber v3 automatic capture by binding method/error preservation. All with auto handling loses structured validator details; JSON/Body struct-validation failures remain raw errors and default to 500. Applications must preserve the error and map to 400/422, or use explicit capture before conversion.
4. Section 13: retain the proposed context storage and Gin-specific helper wrappers; this POC confirms them.

Source precedents, pinned to tested releases: Echo [v4 RequestLogger](https://github.com/labstack/echo/blob/v4.16.0/middleware/request_logger.go), [v5 RequestLogger](https://github.com/labstack/echo/blob/v5.4.0/middleware/request_logger.go); Fiber [v2 logger](https://github.com/gofiber/fiber/blob/v2.52.15/middleware/logger/logger.go), [v3 logger](https://github.com/gofiber/fiber/blob/v3.5.0/middleware/logger/logger.go), [v3 default logger](https://github.com/gofiber/fiber/blob/v3.5.0/middleware/logger/default_logger.go), [v3 binding](https://github.com/gofiber/fiber/blob/v3.5.0/bind.go). Tests and source establish the behaviors above; no production SDK or OTLP pipeline is exercised.
