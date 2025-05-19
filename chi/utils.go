package apitally

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/go-chi/chi/v5"
)

func getRoutes(r chi.Router) []common.PathInfo {
	var paths []common.PathInfo
	walkFn := func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		paths = append(paths, common.PathInfo{
			Method: method,
			Path:   route,
		})
		return nil
	}
	chi.Walk(r, walkFn)
	return paths
}

func getVersions(appVersion string) map[string]string {
	versions := map[string]string{
		"go": runtime.Version(),
		// Chi currently doesn't expose version info
	}
	if appVersion != "" {
		versions["app"] = strings.TrimSpace(appVersion)
	}
	return versions
}

func getRoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	return rctx.RoutePattern()
}
