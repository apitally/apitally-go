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
> **Upgrading from 0.x?** Version 1.0 is a full rewrite with a new setup API. See the [migration guide](MIGRATION.md) for how to upgrade.

## Key features

- **API analytics**: Traffic, error and performance metrics for your API, each endpoint, and per API consumer. Drill down from metrics to individual API requests.
- **Request logs and traces**: Every request as a searchable log entry, with optional capture of headers and request/response bodies. Requests are exported as OpenTelemetry spans, including spans from any other instrumentations you have.
- **Application logs**: Logs written via `log/slog` can be captured and correlated with the requests they belong to.
- **Error tracking**: Validation errors and server errors, including panics with stack traces.
- **Server metrics**: CPU and memory usage of your app's processes.
- **API monitoring & alerts**: Get notified if something isn't right using custom alerts, synthetic uptime checks and heartbeat monitoring. Alert notifications can be delivered via email, Slack and Microsoft Teams.

## Supported frameworks

The SDK supports **Go** `>= 1.25`.

| Framework                                     | Supported versions    | Setup guide                                                             |
| --------------------------------------------- | --------------------- | ----------------------------------------------------------------------- |
| [**Gin**](https://github.com/gin-gonic/gin)   | `v1.9.1+`             | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/gin)   |
| [**Fiber**](https://github.com/gofiber/fiber) | `v2.51.0+`, `v3.0.0+` | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/fiber) |
| [**Echo**](https://github.com/labstack/echo)  | `v4.11.4+`, `v5.0.4+` | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/echo)  |
| [**Chi**](https://github.com/go-chi/chi)      | `v5.1.0+`             | [Link](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/chi)   |

Apitally also supports many other web frameworks in [JavaScript](https://github.com/apitally/apitally-js), [Python](https://github.com/apitally/apitally-py) and [.NET](https://github.com/apitally/apitally-dotnet) via our other SDKs.

## Getting started

If you don't have an Apitally account yet, first [sign up here](https://app.apitally.io/?signup). Then create an app in the Apitally dashboard. You'll see detailed setup instructions with code snippets you can copy and paste. These also include your write token.

### Gin

Install the SDK module for Gin:

```bash
go get github.com/apitally/apitally-go/gin-v1
```

Call `apitally.Init` right after `gin.Default()`, before registering middleware and routes:

```go
import (
    "github.com/gin-gonic/gin"

    apitally "github.com/apitally/apitally-go/gin-v1"
)

func main() {
    r := gin.Default()

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token"
    cfg.Env = "dev"
    apitally.Init(r, cfg)

    // register middleware and routes below this point
}
```

For further instructions, see our [setup guide for Gin](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/gin).

### Fiber

Install the SDK module for Fiber:

```bash
go get github.com/apitally/apitally-go/fiber-v3
```

Call `apitally.Init` right after registering Fiber's `recover` middleware, before registering other middleware and routes:

```go
import (
    "github.com/gofiber/fiber/v3"
    "github.com/gofiber/fiber/v3/middleware/recover"

    apitally "github.com/apitally/apitally-go/fiber-v3"
)

func main() {
    app := fiber.New()
    app.Use(recover.New())

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token"
    cfg.Env = "dev"
    apitally.Init(app, cfg)

    // register middleware and routes below this point
}
```

For further instructions, see our [setup guide for Fiber](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/fiber).

### Echo

Install the SDK module for Echo:

```bash
go get github.com/apitally/apitally-go/echo-v5
```

Call `apitally.Init` right after registering Echo's `Recover` middleware, before registering other middleware and routes:

```go
import (
    "github.com/labstack/echo/v5"
    "github.com/labstack/echo/v5/middleware"

    apitally "github.com/apitally/apitally-go/echo-v5"
)

func main() {
    e := echo.New()
    e.Use(middleware.Recover())

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token"
    cfg.Env = "dev"
    apitally.Init(e, cfg)

    // register middleware and routes below this point
}
```

For further instructions, see our [setup guide for Echo](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/echo).

### Chi

Install the SDK module for Chi:

```bash
go get github.com/apitally/apitally-go/chi-v5
```

Call `apitally.Init` right after registering Chi's `Recoverer` middleware, before registering other middleware and routes:

```go
import (
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"

    apitally "github.com/apitally/apitally-go/chi-v5"
)

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Recoverer)

    cfg := apitally.NewConfig()
    cfg.WriteToken = "your-write-token"
    cfg.Env = "dev"
    apitally.Init(r, cfg)

    // register middleware and routes below this point
}
```

For further instructions, see our [setup guide for Chi](https://docs.apitally.io/sdk-reference/go/v1/setup-guides/chi).

## Graceful shutdown

Call `apitally.Shutdown` after your HTTP server has stopped, so Apitally can send any remaining telemetry before your app exits:

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

Set configuration options as fields on the `Config` you pass to `apitally.Init`. Always create it with `apitally.NewConfig()`, which sets the defaults. See the [SDK reference](https://docs.apitally.io/sdk-reference/go/v1/configuration) for all available options.

### Environment variables

To keep configuration out of your code, set these environment variables instead of the matching fields:

- `APITALLY_WRITE_TOKEN`: the write token (`WriteToken`)
- `APITALLY_ENV`: the environment name (`Env`), defaults to `dev`
- `APITALLY_DISABLED`: set to `true` to disable Apitally (`Disabled`)

### Request and response data

Apitally captures response headers by default. To also capture request headers and request and response bodies, set these fields:

```go
cfg.CaptureRequestHeaders = true
cfg.CaptureRequestBody = true
cfg.CaptureResponseBody = true
```

Apitally automatically masks common sensitive values, such as `password` fields and the `Authorization` header. To mask additional values, set `MaskQueryParams`, `MaskHeaders`, and `MaskBodyFields` to regular expressions. See our [documentation on masking](https://docs.apitally.io/sdk-reference/go/v1/masking) for details.

### Exclusions and sampling

Apitally automatically excludes common health checks and static assets, such as `/healthz` and `/robots.txt`, from logs and traces. To exclude additional paths, set `ExcludePaths` to regular expressions.

To capture logs and traces for only a fraction of the remaining requests, set `SampleRate`, for example to `0.1` for 10%. To decide for each request individually, set `SampleOnRequest` or `SampleOnResponse` to a callback.

Metrics always include all requests. See our [documentation on sampling](https://docs.apitally.io/sdk-reference/go/v1/sampling) for details.

## Adding context to requests

The following functions add context to the current request. Call them while handling a request, for example in a handler, middleware, or authentication code. Their first argument is the request context, which is `c` in Gin and Fiber, `c.Request().Context()` in Echo, and `r.Context()` in Chi.

### Consumers

To associate the request with the API consumer making it, call `SetConsumer`. Only `Identifier` is required.

```go
apitally.SetConsumer(ctx, apitally.Consumer{
    Identifier: user.Identifier,
    Name:       user.Name,
    Group:      user.Group,
    Attributes: map[string]string{"plan": user.Plan},
})
```

### Custom attributes

To attach custom data to the request's trace, call `SetRequestAttributes`. The `SampleOnResponse`, `MaskRequestBody`, and `MaskResponseBody` callbacks can also read these attributes.

```go
import "go.opentelemetry.io/otel/attribute"

apitally.SetRequestAttributes(ctx, attribute.String("tenant.id", tenantID))
```

See our [documentation on attributes](https://docs.apitally.io/sdk-reference/go/v1/attributes#set-custom-attributes) for details.

### Handled errors

To capture an error that your code handles itself, call `CaptureError`. Panics and errors passed to the framework are captured automatically.

```go
if err := processOrder(ctx); err != nil {
    apitally.CaptureError(ctx, err)
}
```

## Capturing application logs

To capture logs written with `log/slog`, wrap your handler with `apitally.NewSlogHandler`. It passes every record to your handler unchanged.

```go
handler := slog.NewJSONHandler(os.Stdout, nil)
slog.SetDefault(slog.New(apitally.NewSlogHandler(handler)))
```

Don't wrap `slog.Default().Handler()`, because setting the result as the default logger causes a deadlock.

Apitally only captures records logged with the request context, for example with `slog.InfoContext`:

```go
slog.InfoContext(ctx, "Order created", "order_id", order.ID)
```

To mask or drop individual log records, set `MaskLogRecord` to a callback. See our [documentation on masking](https://docs.apitally.io/sdk-reference/go/v1/masking#log-record-masking-callback) for details.

## Existing OpenTelemetry setup

If your app already uses OpenTelemetry, register your tracer provider with `otel.SetTracerProvider` and any HTTP instrumentation middleware, such as `otelgin`, before calling `apitally.Init`. Apitally adds its span processor to your tracer provider and keeps your existing exporters. It also adds its data to your instrumentation's request spans instead of creating its own.

Your tracer provider's sampler also applies to Apitally. Requests it drops have no request logs or traces in Apitally, but still count in metrics.

## Trusted proxies

If your app runs behind a reverse proxy or load balancer, configure trusted proxies in your framework:

- **Gin**: `SetTrustedProxies` method
- **Fiber**: `TrustProxy` and `ProxyHeader` settings
- **Echo**: `IPExtractor` setting
- **Chi**: `RealIP` middleware

Apitally records the client IP your framework reports and uses it for GeoIP.

## Getting help

If you need help please [create a new discussion](https://github.com/orgs/apitally/discussions/categories/q-a) on GitHub or email us at [support@apitally.io](mailto:support@apitally.io). We'll get back to you as soon as possible.

## License

This library is licensed under the terms of the [MIT license](LICENSE).
