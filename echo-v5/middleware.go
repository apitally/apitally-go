// Package apitally integrates Apitally with Echo v5.
package apitally

import (
	"github.com/labstack/echo/v5"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "echo",
	ModulePath: "github.com/labstack/echo/v5",
	ScopeName:  "github.com/apitally/apitally-go/echo-v5",
}

// Init instruments the Echo instance with Apitally. Register recovery
// middleware first, then call Init, then register other middleware and
// routes. A nil cfg is equivalent to NewConfig(). Init copies cfg, so later
// changes to it have no effect. Calling Init again for the same Echo instance
// does nothing.
func Init(e *echo.Echo, cfg *Config) {
	internal.Register(cfg, framework, func() []internal.Route { return listRoutes(e) })
	internal.InstallOnce(e, func() { e.Use(middleware) })
}

func middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		// When the writer unwraps to echo.Response, Context.JSON sets the status
		// on it without calling WriteHeader, so the observing writer goes inside.
		w := c.Response()
		resp, err := echo.UnwrapResponse(w)
		if err == nil {
			w = resp.ResponseWriter
		}
		o := internal.BeginNetHTTP(w, c.Request())
		c.SetRequest(o.Request)
		if err == nil {
			resp.ResponseWriter = o.Writer
		} else {
			c.SetResponse(o.Writer)
		}
		defer func() {
			p := recover()
			o.Finish(routePath(c), c.RealIP(), o.Writer.StatusCode(), p)
			if p != nil {
				panic(p)
			}
		}()
		// Dispatching the error here lets Apitally observe the error handler's
		// response; returning nil keeps Echo from dispatching it a second time.
		if err := next(c); err != nil {
			o.State.CaptureReturnedError(err)
			c.Echo().HTTPErrorHandler(c, err)
		}
		return nil
	}
}

// routePath returns the matched route, or "" when no route matched,
// including requests handled by RouteNotFound registrations.
func routePath(c *echo.Context) string {
	route := c.RouteInfo()
	if route.Method == echo.RouteNotFound {
		return ""
	}
	return route.Path
}

// listRoutes omits RouteAny registrations, which match any method, and
// RouteNotFound registrations, which are not routes.
func listRoutes(e *echo.Echo) []internal.Route {
	var routes []internal.Route
	for _, route := range e.Router().Routes() {
		if route.Method != echo.RouteAny && route.Method != echo.RouteNotFound {
			routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
		}
	}
	return routes
}
