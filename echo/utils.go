package apitally

import (
	"runtime"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/labstack/echo/v4"
)

func getRoutes(e *echo.Echo) []common.PathInfo {
	routes := e.Routes()
	paths := make([]common.PathInfo, 0, len(routes))

	for _, route := range routes {
		paths = append(paths, common.PathInfo{
			Method: route.Method,
			Path:   route.Path,
		})
	}

	return paths
}

func getVersions(appVersion string) map[string]string {
	versions := map[string]string{
		"go": runtime.Version(),
		// Echo doesn't expose version info like Gin/Fiber
	}

	if appVersion != "" {
		versions["app"] = strings.TrimSpace(appVersion)
	}

	return versions
}

func getRoutePattern(c echo.Context) string {
	if c.Path() != "" {
		return c.Path()
	}
	return ""
}
