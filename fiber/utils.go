package apitally

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/gofiber/fiber/v2"
)

var excludedMethods = []string{"HEAD", "OPTIONS", "CONNECT", "TRACE"}

func getRoutes(app *fiber.App) []common.PathInfo {
	fiberRoutes := app.GetRoutes()
	paths := make([]common.PathInfo, 0, len(fiberRoutes))

	for _, route := range fiberRoutes {
		if !slices.Contains(excludedMethods, route.Method) && route.Path != "/" {
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
		"go":    runtime.Version(),
		"fiber": fiber.Version,
	}

	if appVersion != "" {
		versions["app"] = strings.TrimSpace(appVersion)
	}

	return versions
}

func getFullURL(c *fiber.Ctx) string {
	scheme := "http"
	if c.Protocol() == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, c.Hostname(), c.OriginalURL())
}

func transformHeaders(header map[string][]string) [][2]string {
	headers := make([][2]string, 0)
	for k, values := range header {
		for _, v := range values {
			headers = append(headers, [2]string{k, v})
		}
	}
	return headers
}
