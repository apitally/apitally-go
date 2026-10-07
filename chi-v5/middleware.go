// Package apitally integrates Apitally with Chi.
package apitally

import (
	"net/http"

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
	internal.InstallOnce(r, func() { r.Use(internal.NetHTTPMiddleware(routePattern)) })
}

func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.RoutePattern()
	}
	return ""
}

func listRoutes(r chi.Router) []internal.Route {
	var routes []internal.Route
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, internal.Route{Method: method, Path: route})
		return nil
	})
	return routes
}
