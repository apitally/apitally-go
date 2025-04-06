package gin

import (
	"runtime"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
)

func UseApitally(r *gin.Engine, config *ApitallyConfig) {
	client, err := internal.NewApitallyClient(*config)
	if err != nil {
		panic(err)
	}

	r.Use(ApitallyMiddleware(client))

	// Delay startup data collection to ensure all routes are registered
	go func() {
		time.Sleep(time.Second)
		client.SetStartupData(getRoutes(r), getVersions(config.AppVersion), "go:gin")
	}()
}

func getRoutes(r *gin.Engine) []common.PathInfo {
	routes := r.Routes()
	paths := make([]common.PathInfo, 0, len(routes))

	for _, route := range routes {
		paths = append(paths, common.PathInfo{
			Method: route.Method,
			Path:   route.Path,
		})
	}

	return paths
}

func getVersions(appVersion *string) map[string]string {
	versions := map[string]string{
		"go":  runtime.Version(),
		"gin": gin.Version,
	}

	if appVersion != nil {
		versions["app"] = *appVersion
	}

	return versions
}
