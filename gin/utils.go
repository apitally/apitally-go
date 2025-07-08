package apitally

import (
	"runtime"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/gin-gonic/gin"
)

func getRoutes(r *gin.Engine) []common.PathInfo {
	routes := r.Routes()
	paths := make([]common.PathInfo, 0, len(routes))

	for _, route := range routes {
		if route.Method != "OPTIONS" && route.Method != "HEAD" {
			paths = append(paths, common.PathInfo{
				Method: route.Method,
				Path:   route.Path,
			})
		}
	}

	return paths
}

func getVersions(appVersion string) map[string]string {
	versions := map[string]string{
		"go":       runtime.Version(),
		"gin":      gin.Version,
		"apitally": common.Version,
	}

	if appVersion != "" {
		versions["app"] = strings.TrimSpace(appVersion)
	}

	return versions
}
