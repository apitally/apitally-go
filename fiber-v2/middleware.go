// Package apitally integrates Apitally with Fiber v2.
package apitally

import (
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "fiber",
	ModulePath: "github.com/gofiber/fiber/v2",
	ScopeName:  "github.com/apitally/apitally-go/fiber-v2",
}

// Init instruments the app with Apitally. Register recovery middleware
// first, then call Init, then register other middleware, groups and routes.
// Routes registered before Init are not monitored. A nil cfg is equivalent
// to NewConfig(). Init copies cfg, so later changes to it have no effect.
// Calling Init again for the same app does nothing.
//
// Shutting down the app delivers the telemetry recorded so far, waiting at
// most 5 seconds. Call Shutdown when the application exits.
func Init(app *fiber.App, cfg *Config) {
	internal.Register(cfg, framework, func() []internal.Route { return listRoutes(app) })
	internal.InstallOnce(app, func() {
		if len(app.GetRoutes(true)) > 0 {
			internal.LogLateInitError()
		}
		app.Use(newMiddleware(app))
		app.Hooks().OnListen(func(fiber.ListenData) error {
			// With prefork, OnListen runs only in the master process, which serves
			// no requests. Each child process activates on its first request.
			if !app.Config().Prefork {
				internal.Activate()
			}
			return nil
		})
		app.Hooks().OnShutdown(func() error {
			internal.Flush()
			return nil
		})
	})
}

func newMiddleware(app *fiber.App) fiber.Handler {
	// Routes are registered before the first request; middleware routes are
	// not routes.
	routes := sync.OnceValue(func() map[internal.Route]bool {
		routes := map[internal.Route]bool{}
		for _, route := range app.GetRoutes(true) {
			routes[internal.Route{Method: route.Method, Path: route.Path}] = true
		}
		return routes
	})
	return func(c *fiber.Ctx) error {
		o, ctx := internal.BeginFasthttp(c.UserContext(), c.Context(), requestInfo(c))
		c.SetUserContext(ctx)
		if o.State != nil {
			// Context.Value of fasthttp.RequestCtx resolves string keys to user values,
			// which include Locals.
			c.Locals(internal.RequestStateKey, o.State)
		}
		defer func() {
			p := recover()
			if o.State != nil {
				// Fiber reuses the context after the middleware returns, so the
				// response data is copied now.
				route := c.Route()
				result := internal.TransportResult{
					StatusCode:     c.Response().StatusCode(),
					ClientAddress:  strings.Clone(c.IP()),
					ResponseHeader: internal.HeaderFromValues(c.GetRespHeaders()),
				}
				if routes()[internal.Route{Method: route.Method, Path: route.Path}] {
					result.Route = strings.Clone(route.Path)
				}
				o.FinishHandler(result, c.Request(), c.Response(), p)
			}
			if p != nil {
				panic(p)
			}
		}()
		// Dispatching the error here lets Apitally observe the error handler's
		// response; returning nil keeps Fiber from dispatching it a second time.
		if err := c.Next(); err != nil {
			o.State.CaptureError(err)
			if c.App().ErrorHandler(c, err) != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}
		return nil
	}
}

// requestInfo copies the request data, because Fiber reuses its buffers.
func requestInfo(c *fiber.Ctx) internal.RequestInfo {
	return internal.RequestInfo{
		Method:        strings.Clone(c.Method()),
		Scheme:        strings.Clone(c.Protocol()),
		Host:          string(c.Request().Host()),
		Path:          strings.Clone(c.Path()),
		Query:         string(c.Request().URI().QueryString()),
		Header:        internal.HeaderFromValues(c.GetReqHeaders()),
		ContentLength: max(int64(c.Request().Header.ContentLength()), -1),
	}
}

func listRoutes(app *fiber.App) []internal.Route {
	var routes []internal.Route
	for _, route := range app.GetRoutes(true) {
		routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
	}
	return routes
}
