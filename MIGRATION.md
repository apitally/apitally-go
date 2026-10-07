# Migrating from 0.x to 1.x

This guide is also available in the [Apitally documentation](https://docs.apitally.io/sdk-reference/go/v1/migration).

The Go SDK now uses OpenTelemetry to collect and send metrics, logs, and traces.

> [!WARNING]
> Request logging, tracing, and application log capture are now enabled by default. If you previously used the SDK for metrics only, set `SampleRate = 0` to keep that behavior.

## Installation and setup

The SDK now requires Go 1.25 or later. Upgrade the module for your framework:

```bash
go get github.com/apitally/apitally-go/chi-v5@v1    # or echo-v4, echo-v5, fiber-v2, fiber-v3
go get github.com/apitally/apitally-go/gin-v1@v1    # replaces github.com/apitally/apitally-go/gin
```

The Gin module has been renamed from `github.com/apitally/apitally-go/gin` to `github.com/apitally/apitally-go/gin-v1`. Update your imports and remove the old module with `go mod tidy`.

### Write tokens replace client IDs

The SDK now authenticates with a **write token** instead of a client ID. Your existing app's token (`apt_...`) is available under _Setup instructions_ in the [Apitally dashboard](https://app.apitally.io/apps).

`NewConfig()` no longer takes a client ID. Set the `WriteToken` option, or set the `APITALLY_WRITE_TOKEN` environment variable. A missing or invalid token no longer fails silently in the background. Instead, the SDK logs an error and disables itself.

### `Init` replaces the middleware

Replace the `Middleware` registration with a call to `Init`, which registers everything the SDK needs. Call it after your recovery middleware and before your other middleware, groups and routes. On Gin and Fiber, routes registered before `Init` are not monitored, and `Init` logs an error when it finds them.

```go
// Before
r := gin.Default()
config := apitally.NewConfig("your-client-id")
config.Env = "dev"
r.Use(apitally.Middleware(r, config))

// After
r := gin.Default()
cfg := apitally.NewConfig()
cfg.WriteToken = "your-write-token"
cfg.Env = "dev"
apitally.Init(r, cfg)
```

### Graceful shutdown

Call the new `apitally.Shutdown` function when your application exits, after your HTTP server has stopped, so the SDK can deliver the remaining telemetry. Without it, up to one export interval of telemetry is lost at exit. See the [README](README.md#graceful-shutdown) for the full pattern.

```go
srv.Shutdown(ctx)
apitally.Shutdown(ctx)
```

## Configuration changes

The `RequestLoggingConfig` type and the `RequestLogging` field have been removed. Their settings are now fields directly on `Config`, with the changes listed below.

```go
// Before
config := apitally.NewConfig("your-client-id")
config.RequestLogging = &apitally.RequestLoggingConfig{
    Enabled:            true,
    LogRequestBody:     true,
    LogResponseBody:    true,
    MaskHeaders:        []*regexp.Regexp{regexp.MustCompile(`(?i)^X-Internal-`)},
}

// After
cfg := apitally.NewConfig()
cfg.WriteToken = "your-write-token"
cfg.CaptureRequestBody = true
cfg.CaptureResponseBody = true
cfg.MaskHeaders = []string{`^X-Internal-`}
```

### Changed options

| Option | Change |
| --- | --- |
| `ClientID` | Replaced by `WriteToken`, which requires a new credential. |
| `Env` | Can now also be set with the `APITALLY_ENV` environment variable. |
| `RequestLogging.CaptureLogs` | Moved to `CaptureLogs`. Default changed from `false` to `true`. Logs are captured through `NewSlogHandler` (see below). |
| `RequestLogging.LogRequestHeaders` | Renamed to `CaptureRequestHeaders`. |
| `RequestLogging.LogRequestBody` | Renamed to `CaptureRequestBody`. |
| `RequestLogging.LogResponseHeaders` | Renamed to `CaptureResponseHeaders`. |
| `RequestLogging.LogResponseBody` | Renamed to `CaptureResponseBody`. |
| `RequestLogging.MaskQueryParams` | Moved to `MaskQueryParams`, now `[]string`. |
| `RequestLogging.MaskHeaders` | Moved to `MaskHeaders`, now `[]string`. |
| `RequestLogging.MaskBodyFields` | Moved to `MaskBodyFields`, now `[]string`. |
| `RequestLogging.ExcludePaths` | Moved to `ExcludePaths`, now `[]string`. |
| `RequestLogging.MaskRequestBodyCallback` | Replaced by `MaskRequestBody` with new arguments. |
| `RequestLogging.MaskResponseBodyCallback` | Replaced by `MaskResponseBody` with new arguments. |
| `RequestLogging.ExcludeCallback` | Replaced by `SampleOnRequest` or `SampleOnResponse` with new arguments and return values. |

Pattern options now take regular expressions as strings instead of `*regexp.Regexp` values. They are matched case-insensitively unless a pattern sets its own flags, such as `(?-i:...)`.

### Removed options

| Removed option | Migration |
| --- | --- |
| `RequestLogging` | Set its fields directly on `Config`, applying the changes above. |
| `RequestLogging.Enabled` and `RequestLogging.CaptureTraces` | Request logging and tracing are now enabled by default. Use `SampleRate = 0` to disable request logs and traces. |
| `RequestLogging.LogQueryParams` | Query parameters are now always captured. To mask all values, use `MaskQueryParams = []string{".*"}`. |
| `RequestLogging.LogPanic` | Panics are now always captured in request traces. |
| `DisableSync` | Use the `Disabled` option or the `APITALLY_DISABLED` environment variable. Apitally is also disabled in `go test` binaries. |

The [configuration reference](https://docs.apitally.io/sdk-reference/go/v1/configuration) lists all available options.

## Request helpers

The request helpers now take a `context.Context` instead of the framework's request or context type. This changes the call sites on Echo, Fiber v2 and Chi:

| Framework | Before | After |
| --- | --- | --- |
| Gin | `apitally.SetConsumer(c, ...)` | `apitally.SetConsumer(c, ...)` |
| Echo | `apitally.SetConsumer(c, ...)` | `apitally.SetConsumer(c.Request().Context(), ...)` |
| Fiber v3 | `apitally.SetConsumer(c, ...)` | `apitally.SetConsumer(c, ...)` |
| Fiber v2 | `apitally.SetConsumer(c, ...)` | `apitally.SetConsumer(c.UserContext(), ...)` or `c.Context()` |
| Chi | `apitally.SetConsumer(r, ...)` | `apitally.SetConsumer(r.Context(), ...)` |

The same applies to `CaptureValidationError`.

`SetConsumerIdentifier` has been removed. Use `SetConsumer` with only the `Identifier` field set. The `Consumer` type has a new `Attributes` field for custom consumer attributes.

```go
// Before
apitally.SetConsumerIdentifier(c, user.ID)

// After
apitally.SetConsumer(ctx, apitally.Consumer{Identifier: user.ID})
```

The new `CaptureError` and `SetRequestAttributes` functions capture a handled error and attach custom attributes to the current request.

The `ApitallyMiddleware`, `ApitallyConfig` and `ApitallyConsumer` aliases and the `Request` and `Response` types have been removed.

## Application logs

Log capture now works with `log/slog`. Wrap your handler with `NewSlogHandler` and log with the request context, for example with `slog.InfoContext`:

```go
slog.SetDefault(slog.New(apitally.NewSlogHandler(slog.NewJSONHandler(os.Stdout, nil))))

slog.InfoContext(ctx, "Order created", "order_id", order.ID)
```

Records logged without the request context are not linked to requests and are not captured. See the [README](README.md#logging) for the context to pass on each framework.

## Body masking callbacks

`MaskRequestBody` and `MaskResponseBody` now both receive `(span, body)`, rather than the `Request` and `Response` objects. The body is passed after decompression. Return the body to capture, or `nil` to capture `[REDACTED]`.

Callbacks may run later on another goroutine against an ended span. Request metadata is available through [`span.Attributes()`](https://docs.apitally.io/sdk-reference/go/v1/attributes).

```go
// Before
config.RequestLogging.MaskRequestBodyCallback = func(request *apitally.Request) []byte {
    if strings.HasPrefix(request.Path, "/admin/") {
        return nil
    }
    return request.Body
}

// After
cfg.MaskRequestBody = func(span sdktrace.ReadOnlySpan, body []byte) []byte {
    for _, kv := range span.Attributes() {
        if kv.Key == "http.route" && strings.HasPrefix(kv.Value.AsString(), "/admin/") {
            return nil
        }
    }
    return body
}
```

## Request exclusion

Use sampling callbacks to exclude requests: `SampleOnRequest` for early decisions based on the request, or `SampleOnResponse` for decisions based on the response status or consumer. Both receive the span as their only argument.

The callbacks return a keep probability and `true`: `1, true` captures the request, and `0, true` excludes it. Returning `ok == false` from `SampleOnRequest` applies `SampleRate`, and from `SampleOnResponse` preserves the earlier sampling decision. Note that the meaning is reversed from `ExcludeCallback`.

For example, to capture only error responses:

```go
// Before
config.RequestLogging.ExcludeCallback = func(request *apitally.Request, response *apitally.Response) bool {
    return response.StatusCode < 400
}

// After
cfg.SampleOnResponse = func(span sdktrace.ReadOnlySpan) (float64, bool) {
    for _, kv := range span.Attributes() {
        if kv.Key == "http.response.status_code" && kv.Value.AsInt64() >= 400 {
            return 1, true
        }
    }
    return 0, true
}
```

Captured headers and bodies are not available in sampling callbacks. Sampling affects request logs and traces, but not metrics.

### Path exclusions

`ExcludePaths` matches request paths, not route patterns. If a pattern contains route parameters, update it to match concrete values. For example, replace `^/users/:id$` with `^/users/[^/]+$` to match `/users/123`.

## Other changes

- **Network access:** The SDK now sends data to `otlp.apitally.io` instead of `hub.apitally.io`. Update firewall allowlists if necessary.
- **Existing OpenTelemetry setups:** If your application registers a `go.opentelemetry.io/otel/sdk/trace` tracer provider with `otel.SetTracerProvider`, the SDK adds its span processor to it. Its sampler then also affects Apitally's request logs and traces.
