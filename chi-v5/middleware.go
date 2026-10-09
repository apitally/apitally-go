// Package apitally integrates Apitally with Chi.
package apitally

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "chi",
	ModulePath: "github.com/go-chi/chi/v5",
	ScopeName:  "github.com/apitally/apitally-go/chi-v5",
}

// Init instruments the router with Apitally. Register recovery middleware
// first, then call Init, then register other middleware and routes. A nil cfg
// is equivalent to NewConfig(). Init copies cfg, so later changes to it have
// no effect. Calling Init again for the same router does nothing.
func Init(r chi.Router, cfg *Config) {
	internal.Register(cfg, framework, func() []internal.Route { return listRoutes(r) })
	internal.InstallOnce(r, func() { r.Use(internal.NetHTTPMiddleware(newRoutePattern(r))) })
}

// newRoutePattern returns the matched route template of a request, or "".
// For an unmatched request below a mounted router, Chi reports the mount
// pattern, such as "/api/*", which is not a route.
func newRoutePattern(router chi.Router) func(*http.Request) string {
	isRegistered := internal.NewRouteSet(func() []internal.Route { return listRoutes(router) })
	return func(r *http.Request) string {
		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			return ""
		}
		// RouteMethod is the method Chi routed by, which middleware.GetHead sets to
		// GET for HEAD requests.
		if pattern := rctx.RoutePattern(); isRegistered(internal.Route{Method: rctx.RouteMethod, Path: pattern}) {
			return pattern
		}
		return ""
	}
}

// listRoutes reports routes as Chi's RoutePattern does. RoutePattern trims
// the trailing slash that Walk reports for the root route of a subrouter, and
// repeats the removal of mount wildcards, which Walk does once.
func listRoutes(r chi.Router) []internal.Route {
	var routes []internal.Route
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		for strings.Contains(route, "/*/") {
			route = strings.ReplaceAll(route, "/*/", "/")
		}
		if route != "/" {
			route = strings.TrimSuffix(strings.TrimSuffix(route, "//"), "/")
		}
		routes = append(routes, internal.Route{Method: method, Path: route})
		return nil
	})
	return routes
}
