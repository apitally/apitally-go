package apitally

import (
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/apitally/apitally-go/common"
	"github.com/gin-gonic/gin"
)

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

func getVersions(appVersion string) map[string]string {
	versions := map[string]string{
		"go":  runtime.Version(),
		"gin": gin.Version,
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

func getFullURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	host := req.Host
	if host == "" {
		host = req.Header.Get("Host")
	}

	return fmt.Sprintf("%s://%s%s", scheme, host, req.URL.String())
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
	headers := make([][2]string, 0, len(header))
	for k, v := range header {
		if len(v) > 0 {
			headers = append(headers, [2]string{k, v[0]})
		}
	}
	return headers
}
