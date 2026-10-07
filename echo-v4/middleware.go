// Package apitally integrates Apitally with Echo v4.
package apitally

import (
	"sync"

	"github.com/labstack/echo/v4"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "echo",
	ModulePath: "github.com/labstack/echo/v4",
	ScopeName:  "github.com/apitally/apitally-go/echo-v4",
}

// Init instruments the Echo instance with Apitally. Register recovery
// middleware first, then call Init, then register other middleware and
// routes. A nil cfg is equivalent to NewConfig(). Init copies cfg, so later
// changes to it have no effect. Calling Init again for the same Echo instance
// does nothing.
func Init(e *echo.Echo, cfg *Config) {
	internal.Register(cfg, framework, func() []internal.Route { return listRoutes(e) })
	internal.InstallOnce(e, func() { e.Use(newMiddleware(e)) })
}

func newMiddleware(e *echo.Echo) echo.MiddlewareFunc {
	// Routes are registered before the first request.
	routes := sync.OnceValue(func() map[internal.Route]bool {
		routes := map[internal.Route]bool{}
		for _, route := range listRoutes(e) {
			routes[route] = true
		}
		return routes
	})
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			o := internal.BeginNetHTTP(c.Response().Writer, c.Request())
			c.SetRequest(o.Request)
			c.Response().Writer = o.Writer
			defer func() {
				p := recover()
				// For unmatched requests, Echo can report the path of a route
				// registered for another method or with RouteNotFound, such as
				// those Group.Use adds.
				route := c.Path()
				if !routes()[internal.Route{Method: c.Request().Method, Path: route}] {
					route = ""
				}
				o.Finish(route, c.RealIP(), o.Writer.StatusCode(), p)
				if p != nil {
					panic(p)
				}
			}()
			// Dispatching the error here lets Apitally observe the error handler's
			// response; returning nil keeps Echo from dispatching it a second time.
			if err := next(c); err != nil {
				o.State.CaptureError(err)
				c.Error(err)
			}
			return nil
		}
	}
}

func listRoutes(e *echo.Echo) []internal.Route {
	var routes []internal.Route
	for _, route := range e.Routes() {
		if route.Method != echo.RouteNotFound {
			routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
		}
	}
	return routes
}
