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
<img alt="Apitally screenshots" src="https://assets.apitally.io/screenshots/overview.png">
<br>

# Apitally SDK for Go

[![Tests](https://github.com/apitally/apitally-go/actions/workflows/tests.yaml/badge.svg?event=push)](https://github.com/apitally/apitally-go/actions)
[![Codecov](https://codecov.io/gh/apitally/apitally-go/graph/badge.svg?token=KGMvKb59lc)](https://codecov.io/gh/apitally/apitally-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/apitally/apitally-go.svg)](https://pkg.go.dev/github.com/apitally/apitally-go)

Apitally is a simple API monitoring and analytics tool that makes it easy to understand how your APIs are used
and helps you troubleshoot API issues faster. Setup is easy and takes less than 5 minutes.

Learn more about Apitally on our 🌎 [website](https://apitally.io) or check out
the 📚 [documentation](https://docs.apitally.io).

## Key features

### API analytics

Track traffic, error and performance metrics for your API, each endpoint and
individual API consumers, allowing you to make informed, data-driven engineering
and product decisions.

### Request logs

Drill down from insights to individual API requests or use powerful search and filters to
find specific requests. View correlated application logs and traces for a complete picture
of each request, making troubleshooting faster and easier.

### Error tracking

Understand which validation rules in your endpoints cause client errors. Capture
error details and stack traces for 500 error responses, and have them linked to
Sentry issues automatically.

### API monitoring & alerts

Get notified immediately if something isn't right using custom alerts, synthetic
uptime checks and heartbeat monitoring. Alert notifications can be delivered via
email, Slack and Microsoft Teams.

## Supported frameworks

This SDK requires Go 1.21 or higher.

| Framework                                     | Supported versions | Setup guide                                         |
| --------------------------------------------- | ------------------ | --------------------------------------------------- |
| [**Chi**](https://github.com/go-chi/chi)      | `v5`               | [Link](https://docs.apitally.io/setup-guides/chi)   |
| [**Echo**](https://github.com/labstack/echo)  | `v4`               | [Link](https://docs.apitally.io/setup-guides/echo)  |
| [**Fiber**](https://github.com/gofiber/fiber) | `v2`               | [Link](https://docs.apitally.io/setup-guides/fiber) |
| [**Gin**](https://github.com/gin-gonic/gin)   | `v1`               | [Link](https://docs.apitally.io/setup-guides/gin)   |

Apitally also supports many other web frameworks in [JavaScript](https://github.com/apitally/apitally-js), [Python](https://github.com/apitally/apitally-py), [.NET](https://github.com/apitally/apitally-dotnet) and [Java](https://github.com/apitally/apitally-java) via our other SDKs.

## Getting started

If you don't have an Apitally account yet, first [sign up here](https://app.apitally.io/?signup). Then create an app in the Apitally dashboard. You'll see detailed setup instructions with code snippets you can copy and paste. These also include your client ID.

See the [SDK reference](https://docs.apitally.io/sdk-reference/go) for all available configuration options, including how to mask sensitive data, customize request logging, and more.

### Chi

Add the SDK to your dependencies:

```go
go get github.com/apitally/apitally-go/chi
```

Then add the Apitally middleware to your application:

```go
import (
    apitally "github.com/apitally/apitally-go/chi"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()

    config := apitally.NewConfig("your-client-id")
    config.Env = "dev" // or "prod" etc.

    r.Use(apitally.Middleware(r, config))

    // ... rest of your code ...
}
```

For further instructions, see our
[setup guide for Chi](https://docs.apitally.io/setup-guides/chi).

### Echo

Add the SDK to your dependencies:

```go
go get github.com/apitally/apitally-go/echo
```

Then add the Apitally middleware to your application:

```go
import (
    apitally "github.com/apitally/apitally-go/echo"
    "github.com/labstack/echo/v4"
)

func main() {
    e := echo.New()

    config := apitally.NewConfig("your-client-id")
    config.Env = "dev" // or "prod" etc.

    e.Use(apitally.Middleware(e, config))

    // ... rest of your code ...
}
```

For further instructions, see our
[setup guide for Echo](https://docs.apitally.io/setup-guides/echo).

### Fiber

Add the SDK to your dependencies:

```go
go get github.com/apitally/apitally-go/fiber
```

Then add the Apitally middleware to your application:

```go
import (
    apitally "github.com/apitally/apitally-go/fiber"
    "github.com/gofiber/fiber/v2"
)

func main() {
    app := fiber.New()

    config := apitally.NewConfig("your-client-id")
    config.Env = "dev" // or "prod" etc.

    app.Use(apitally.Middleware(app, config))

    // ... rest of your code ...
}
```

For further instructions, see our
[setup guide for Fiber](https://docs.apitally.io/setup-guides/fiber).

### Gin

Add the SDK to your dependencies:

```go
go get github.com/apitally/apitally-go/gin
```

Then add the Apitally middleware to your application:

```go
import (
    apitally "github.com/apitally/apitally-go/gin"
    "github.com/gin-gonic/gin"
)

func main() {
    r := gin.Default()

    config := apitally.NewConfig("your-client-id")
    config.Env = "dev" // or "prod" etc.

    r.Use(apitally.Middleware(r, config))

    // ... rest of your code ...
}
```

For further instructions, see our
[setup guide for Gin](https://docs.apitally.io/setup-guides/gin).

## Getting help

If you need help please
[create a new discussion](https://github.com/orgs/apitally/discussions/categories/q-a)
on GitHub or email us at [support@apitally.io](mailto:support@apitally.io). We'll get back to you as soon as possible.

## License

This library is licensed under the terms of the [MIT license](LICENSE).
