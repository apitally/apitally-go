package apitally

import (
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
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
		"go":  runtime.Version(),
		"chi": "v5", // Chi doesn't expose version info
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

func getFullURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := r.Host
	if host == "" {
		host = r.Header.Get("Host")
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, r.URL.String())
}

func parseContentLength(contentLength string) int64 {
	if contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			return size
		}
	}
	return -1
}

func transformHeaders(header http.Header) [][2]string {
	headers := make([][2]string, 0)
	for k, values := range header {
		for _, v := range values {
			headers = append(headers, [2]string{k, v})
		}
	}
	return headers
}

func getRoutePattern(r *http.Request) string {
	if routePattern := chi.RouteContext(r.Context()).RoutePattern(); routePattern != "" {
		return routePattern
	}
	return r.URL.Path
}
