<p align="center">
  <a href="https://apitally.io" target="_blank">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://assets.apitally.io/logos/logo-horizontal-new-dark.png">
      <source media="(prefers-color-scheme: light)" srcset="https://assets.apitally.io/logos/logo-horizontal-new-light.png">
      <img alt="Apitally logo" src="https://assets.apitally.io/logos/logo-vertical-light.png" width="220">
    </picture>
  </a>
</p>
<p align="center"><b>API monitoring & analytics made simple</b></p>
<p align="center" style="color: #ccc;">Real-time metrics, request logs, and alerts for your APIs — with just a few lines of code.</p>
<br>
<img alt="Apitally screenshots" src="https://assets.apitally.io/screenshots/overview.png">
<br>

# Apitally SDK for Go

[![Tests](https://github.com/apitally/apitally-go/actions/workflows/tests.yaml/badge.svg?event=push)](https://github.com/apitally/apitally-go/actions)
[![Codecov](https://codecov.io/gh/apitally/apitally-go/graph/badge.svg?token=KGMvKb59lc)](https://codecov.io/gh/apitally/apitally-go)

This SDK for Apitally currently supports the following Go web frameworks:

- [Chi](https://docs.apitally.io/frameworks/chi)
- [Echo](https://docs.apitally.io/frameworks/echo)
- [Fiber](https://docs.apitally.io/frameworks/fiber)
- [Gin](https://docs.apitally.io/frameworks/gin)

Learn more about Apitally on our 🌎 [website](https://apitally.io) or check out
the 📚 [documentation](https://docs.apitally.io).

## Key features

### API analytics

Track traffic, error and performance metrics for your API, each endpoint and
individual API consumers, allowing you to make informed, data-driven engineering
and product decisions.

### Error tracking

Understand which validation rules in your endpoints cause client errors. Capture
error details and stack traces for 500 error responses, and have them linked to
Sentry issues automatically.

### Request logging

Drill down from insights to individual requests or use powerful filtering to
understand how consumers have interacted with your API. Configure exactly what
is included in the logs to meet your requirements.

### API monitoring & alerting

Get notified immediately if something isn't right using custom alerts, synthetic
uptime checks and heartbeat monitoring. Notifications can be delivered via
email, Slack or Microsoft Teams.

## Usage

Our comprehensive [setup guides](https://docs.apitally.io/quickstart) include
all the details you need to get started.

### Chi

This is an example of how to use the Apitally middleware with a Chi
application. For further instructions, see our
[setup guide for Chi](https://docs.apitally.io/frameworks/chi).

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

### Echo

This is an example of how to use the Apitally middleware with an Echo
application. For further instructions, see our
[setup guide for Echo](https://docs.apitally.io/frameworks/echo).

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

### Fiber

This is an example of how to use the Apitally middleware with a Fiber
application. For further instructions, see our
[setup guide for Fiber](https://docs.apitally.io/frameworks/fiber).

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

### Gin

This is an example of how to use the Apitally middleware with a Gin application.
For further instructions, see our
[setup guide for Gin](https://docs.apitally.io/frameworks/gin).

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

## Getting help

If you need help please
[create a new discussion](https://github.com/orgs/apitally/discussions/categories/q-a)
on GitHub or
[join our Slack workspace](https://join.slack.com/t/apitally-community/shared_invite/zt-2b3xxqhdu-9RMq2HyZbR79wtzNLoGHrg).

## License

This library is licensed under the terms of the MIT license.
