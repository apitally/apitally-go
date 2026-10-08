// Package apitally integrates Apitally with Echo v4.
package apitally

import (
	"maps"
	"slices"

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
	isRegistered := internal.NewRouteSet(func() []internal.Route { return listRoutes(e) })
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
				if !isRegistered(internal.Route{Method: c.Request().Method, Path: route}) {
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
				o.State.CaptureReturnedError(err)
				c.Error(err)
			}
			return nil
		}
	}
}

// listRoutes lists the routes of the default router and of the routers that
// Echo.Host creates.
func listRoutes(e *echo.Echo) []internal.Route {
	echoRoutes := e.Routes()
	for _, host := range slices.Sorted(maps.Keys(e.Routers())) {
		echoRoutes = append(echoRoutes, e.Routers()[host].Routes()...)
	}
	var routes []internal.Route
	for _, route := range echoRoutes {
		if route.Method != echo.RouteNotFound {
			routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
		}
	}
	return routes
}
