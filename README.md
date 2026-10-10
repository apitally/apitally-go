<p align="center">
  <a href="https://apitally.io" target="_blank">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://assets.apitally.io/logos/logo-horizontal-new-dark.png">
      <source media="(prefers-color-scheme: light)" srcset="https://assets.apitally.io/logos/logo-horizontal-new-light.png">
      <img alt="Apitally logo" src="https://assets.apitally.io/logos/logo-horizontal-new-light.png" width="220">
    </picture>
  </a>
</p>
<p align="center"><b>API monitoring & analytics made simple</b></p>
<p align="center" style="color: #ccc;">Metrics, logs, traces, and alerts for your APIs — with just a few lines of code.</p>
<br>
<p>
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://assets.apitally.io/screenshots/overview-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="https://assets.apitally.io/screenshots/overview-light.png">
  <img alt="Apitally dashboard" src="https://assets.apitally.io/screenshots/overview-light.png">
</picture>
</p>
<br>

# Apitally SDK for Go

[![Tests](https://github.com/apitally/apitally-go/actions/workflows/tests.yaml/badge.svg?event=push)](https://github.com/apitally/apitally-go/actions)
[![Codecov](https://codecov.io/gh/apitally/apitally-go/graph/badge.svg?token=KGMvKb59lc)](https://codecov.io/gh/apitally/apitally-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/apitally/apitally-go.svg)](https://pkg.go.dev/github.com/apitally/apitally-go)

Apitally is a simple API monitoring and analytics tool that makes it easy to understand API usage, monitor performance, and troubleshoot issues.
Get started in minutes by just adding a few lines of code. No infrastructure changes required, no dashboards to build.

The SDK is an [OpenTelemetry](https://opentelemetry.io) distribution and works alongside an existing OpenTelemetry setup.

Learn more about Apitally on our 🌎 [website](https://apitally.io) or check out the 📚 [documentation](https://docs.apitally.io).

> [!IMPORTANT]
> **Upgrading from 0.x?** Version 1.0 is a full rewrite with a new setup API. See the [migration guide](MIGRATION.md) for a full 0.x to 1.x mapping.

## Key features

- **API analytics**: Traffic, error and performance metrics for your API, each endpoint, and per API consumer. Drill down from metrics to individual API requests.
- **Request logs and traces**: Every request as a searchable log entry, with optional capture of headers and request/response bodies. Requests are exported as OpenTelemetry spans, including spans from any other instrumentations you have.
- **Application logs**: Logs written via `log/slog` are captured and correlated with the requests they belong to.
- **Error tracking**: Validation errors, and server errors with stack traces for panics and errors captured with `CaptureError`.
- **API monitoring & alerts**: Get notified if something isn't right using custom alerts, synthetic uptime checks and heartbeat monitoring. Alert notifications can be delivered via email, Slack and Microsoft Teams.

## Supported frameworks

The SDK supports **Go** `>= 1.25`.

| Framework                                     | Supported versions | Setup guide                                                             |
| --------------------------------------------- | ------------------ | ----------------------------------------------------------------------- |
| [**Chi**](https://github.com/go-chi/chi)      | `v5`               | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/chi)   |
| [**Echo**](https://github.com/labstack/echo)  | `v4`, `v5`         | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/echo)  |
| [**Fiber**](https://github.com/gofiber/fiber) | `v2`, `v3`         | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/fiber) |
| [**Gin**](https://github.com/gin-gonic/gin)   | `v1`               | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/gin)   |

Apitally also supports many other web frameworks in [JavaScript](https://github.com/apitally/apitally-js), [Python](https://github.com/apitally/apitally-py) and [.NET](https://github.com/apitally/apitally-dotnet) via our other SDKs.

## Getting started

If you don't have an Apitally account yet, first [sign up here](https://app.apitally.io/?signup). Then create an app in the Apitally dashboard. You'll see detailed setup instructions with code snippets you can copy and paste. These also include your write token.

Add the module for your framework to your dependencies:

```bash
go get github.com/apitally/apitally-go/chi-v5    # for Chi
go get github.com/apitally/apitally-go/echo-v4   # for Echo v4 (or echo-v5)
go get github.com/apitally/apitally-go/fiber-v2  # for Fiber v2 (or fiber-v3)
go get github.com/apitally/apitally-go/gin-v1    # for Gin
```

Each module provides a package named `apitally`. Register your recovery middleware first, then call `Init`, then register other middleware, groups and routes.

See the [SDK reference](https://docs.apitally.io/sdk-reference/go/v1/configuration) for all available configuration options, including how to mask sensitive data, capture request and response payloads, and more.

### Gin

```go
import (
    "github.com/gin-gonic/gin"

    apitally "github.com/apitally/apitally-go/gin-v1"
)

func main() {
    r := gin.Default()

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token" // or set APITALLY_WRITE_TOKEN
    cfg.Env = "dev"                     // or set APITALLY_ENV
    apitally.Init(r, cfg)

    // ... register your routes ...

    r.Run(":8080")
}
```

For further instructions, see our [setup guide for Gin](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/gin).

### Echo

```go
import (
    "github.com/labstack/echo/v4" // or echo/v5
    "github.com/labstack/echo/v4/middleware"

    apitally "github.com/apitally/apitally-go/echo-v4" // or echo-v5
)

func main() {
    e := echo.New()
    e.Use(middleware.Recover())

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token" // or set APITALLY_WRITE_TOKEN
    cfg.Env = "dev"                     // or set APITALLY_ENV
    apitally.Init(e, cfg)

    // ... register your routes ...

    e.Start(":8080")
}
```

For further instructions, see our [setup guide for Echo](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/echo).

### Fiber

```go
import (
    "github.com/gofiber/fiber/v2" // or fiber/v3
    "github.com/gofiber/fiber/v2/middleware/recover"

    apitally "github.com/apitally/apitally-go/fiber-v2" // or fiber-v3
)

func main() {
    app := fiber.New()
    app.Use(recover.New())

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token" // or set APITALLY_WRITE_TOKEN
    cfg.Env = "dev"                     // or set APITALLY_ENV
    apitally.Init(app, cfg)

    // ... register your routes ...

    app.Listen(":8080")
}
```

For further instructions, see our [setup guide for Fiber](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/fiber).

### Chi

```go
import (
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"

    apitally "github.com/apitally/apitally-go/chi-v5"
)

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Recoverer)

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token" // or set APITALLY_WRITE_TOKEN
    cfg.Env = "dev"                     // or set APITALLY_ENV
    apitally.Init(r, cfg)

    // ... register your middleware and routes ...

    http.ListenAndServe(":8080", r)
}
```

For further instructions, see our [setup guide for Chi](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/chi).

## Graceful shutdown

Apitally sends telemetry in the background at regular intervals. Call `apitally.Shutdown` when your application exits, after your HTTP server has stopped, so the remaining telemetry is delivered. Without it, up to one export interval of telemetry is lost at exit.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: ":8080", Handler: r}
go srv.ListenAndServe()
<-ctx.Done()

shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)      // with Fiber: app.ShutdownWithContext(shutdownCtx)
apitally.Shutdown(shutdownCtx)
```

## Configuration

The write token and environment can also be provided via the `APITALLY_WRITE_TOKEN` and `APITALLY_ENV` environment variables instead of the `WriteToken` and `Env` options. The environment defaults to `dev`.

By default, Apitally captures response headers but not request headers or request and response bodies. You can opt in with options:

```go
cfg := apitally.NewConfig()
cfg.CaptureRequestHeaders = true
cfg.CaptureRequestBody = true
cfg.CaptureResponseBody = true
```

Sensitive values in query parameters, headers, and body fields are masked automatically based on built-in patterns, and you can add your own regular expressions via the `MaskQueryParams`, `MaskHeaders`, and `MaskBodyFields` options.

On high-traffic applications you can capture logs and traces for only a fraction of requests by setting `SampleRate` (e.g. `0.1` for 10%), or decide per request with the `SampleOnRequest` and `SampleOnResponse` callbacks. Metrics always count every request, regardless of sampling.

Application logs written via `log/slog` are captured and correlated with requests once you wrap your handler (see [Logging](#logging)). Use `MaskLogRecord` to transform or drop Apitally's captured copy, or opt out with `CaptureLogs = false`.

See the [SDK reference](https://docs.apitally.io/sdk-reference/go/v1/configuration) for all configuration options.

## Identifying consumers and more

The package provides functions you can call from anywhere in your request handling code, with the request context:

```go
// Associate the current request with an API consumer, with custom attributes
apitally.SetConsumer(ctx, apitally.Consumer{
    Identifier: user.ID,
    Name:       user.Name,
    Group:      user.Group,
    Attributes: map[string]string{"plan": user.Plan},
})

// Attach custom attributes to the current request
apitally.SetRequestAttributes(ctx, attribute.String("tenant", tenantID))

// Capture a handled error for the current request
apitally.CaptureError(ctx, err)

// Report validation errors from go-playground/validator
apitally.CaptureValidationError(ctx, err)

// Create a custom span within the current request
ctx, span := otel.Tracer("bookstore").Start(ctx, "search_books")
defer span.End()
```

The request context to pass depends on your framework:

| Framework | Context                 |
| --------- | ----------------------- |
| Gin       | `c.Request.Context()`   |
| Echo      | `c.Request().Context()` |
| Fiber v3  | `c.Context()`           |
| Fiber v2  | `c.UserContext()`       |
| Chi       | `r.Context()`           |

The handler's `c` on Gin and Fiber v3, and `c.Context()` on Fiber v2, also work for the request helpers and logs.

Panics, errors returned to Echo and Fiber or added with `c.Error` in Gin, and [go-playground/validator](https://github.com/go-playground/validator) errors that reach the framework with a 400 or 422 response are captured automatically. Use the functions above for errors your handlers handle themselves.

For further details, check out our [documentation](https://docs.apitally.io).

## Logging

Apitally captures application logs written with [`log/slog`](https://pkg.go.dev/log/slog) and links them to the request they were logged in. Wrap your handler with `apitally.NewSlogHandler`, which passes every record on to your handler unchanged:

```go
slog.SetDefault(slog.New(apitally.NewSlogHandler(slog.NewJSONHandler(os.Stdout, nil))))
```

Log with the request context (see [the table above](#identifying-consumers-and-more)), so Apitally can link the record to its request. Logs without a request context are not captured.

```go
slog.InfoContext(ctx, "Order created", "order_id", order.ID)
```

Wrapping `slog.Default().Handler()` and installing the result with `slog.SetDefault` deadlocks on the first log call, so wrap a handler such as `slog.NewJSONHandler` or `slog.NewTextHandler` instead.

## Existing OpenTelemetry setup

If your app doesn't already use OpenTelemetry, you don't need to know it's there. The Apitally SDK configures OpenTelemetry automatically. It traces incoming requests and includes spans started with the request context, such as from `otelhttp.NewTransport` or `otelsql`.

If your app registers a `go.opentelemetry.io/otel/sdk/trace` tracer provider with `otel.SetTracerProvider` before the first request, Apitally adds its span processor to your provider, keeping your existing exporters.

Your tracer provider's sampling settings also affect Apitally. Requests excluded by the sampler will not have request logs or traces in Apitally. Metrics still include all requests, regardless of sampling.

### OpenTelemetry HTTP instrumentation

If your app uses OpenTelemetry HTTP instrumentation, such as `otelgin` or `otelhttp`, register its middleware before `Init`, or wrap your handler with `otelhttp.NewHandler`. Apitally then adds its data to the instrumentation's request span instead of creating a second one.

## Trusted proxies

If your application runs behind a reverse proxy or load balancer, configure trusted proxies in your framework so Apitally can record the real client IP for GeoIP. Apitally uses the client IP reported by your framework. It does not read forwarding headers itself to determine the client IP.

## Getting help

If you need help please [create a new discussion](https://github.com/orgs/apitally/discussions/categories/q-a) on GitHub or email us at [support@apitally.io](mailto:support@apitally.io). We'll get back to you as soon as possible.

## License

This library is licensed under the terms of the [MIT license](LICENSE).
