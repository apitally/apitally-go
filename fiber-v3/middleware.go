// Package apitally integrates Apitally with Fiber v3.
package apitally

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "fiber",
	ModulePath: "github.com/gofiber/fiber/v3",
	ScopeName:  "github.com/apitally/apitally-go/fiber-v3",
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
		app.Use(middleware)
		app.Hooks().OnListen(func(data fiber.ListenData) error {
			// With prefork, OnListen runs only in the master process, which serves
			// no requests. Each child process activates on its first request.
			if !data.Prefork {
				internal.Activate()
			}
			return nil
		})
		app.Hooks().OnPostShutdown(func(error) error {
			internal.Flush()
			return nil
		})
	})
}

func middleware(c fiber.Ctx) error {
	o, ctx := internal.BeginFasthttp(c.Context(), c.RequestCtx(), requestInfo(c))
	c.SetContext(ctx)
	if o.State != nil {
		// Context.Value of fiber.Ctx resolves keys to Locals.
		c.Locals(internal.RequestStateKey, o.State)
	}
	defer func() {
		p := recover()
		if o.State != nil {
			// Fiber reuses the context after the middleware returns, so the
			// response data is copied now.
			result := internal.TransportResult{
				StatusCode:     c.Response().StatusCode(),
				ClientAddress:  strings.Clone(c.IP()),
				ResponseHeader: internal.HeaderFromValues(c.GetRespHeaders()),
			}
			// Without a matched route, only middleware ran.
			if c.Matched() {
				result.Route = strings.Clone(c.Route().Path)
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

// requestInfo copies the request data, because Fiber reuses its buffers.
func requestInfo(c fiber.Ctx) internal.RequestInfo {
	return internal.RequestInfo{
		Method:        strings.Clone(c.Method()),
		Scheme:        strings.Clone(c.Scheme()),
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
