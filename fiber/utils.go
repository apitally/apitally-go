package apitally

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/gofiber/fiber/v2"
)

func getRoutes(app *fiber.App) []common.PathInfo {
	fiberRoutes := app.GetRoutes()
	paths := make([]common.PathInfo, 0, len(fiberRoutes))

	for _, route := range fiberRoutes {
		// Filter out auto-generated routes
		if route.Method != "HEAD" && route.Path != "/" {
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

func truncateValidationErrorMessage(msg string) string {
	re := regexp.MustCompile(`^Key: '.+' Error:(.+)$`)
	matches := re.FindStringSubmatch(msg)
	if len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	return msg
}

func getFullURL(c *fiber.Ctx) string {
	scheme := "http"
	if c.Protocol() == "https" {
		scheme = "https"
	}

	return fmt.Sprintf("%s://%s%s", scheme, c.Hostname(), c.OriginalURL())
}

func parseContentLength(contentLength string) int64 {
	if contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			return size
		}
	}
	return -1
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
