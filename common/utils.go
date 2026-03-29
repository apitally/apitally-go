package common

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func TruncateValidationErrorMessage(msg string) string {
	re := regexp.MustCompile(`^Key: '.+' Error:(.+)$`)
	matches := re.FindStringSubmatch(msg)
	if len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	return msg
}

func GetFullURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil || isHTTPS(req.Header) {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, req.Host, req.URL.String())
}

func isHTTPS(header http.Header) bool {
	for _, key := range []string{
		"X-Forwarded-Proto",
		"X-Forwarded-Protocol",
		"X-Forwarded-Scheme",
		"X-Url-Scheme",
		"X-Scheme",
	} {
		if v := header.Get(key); v != "" {
			scheme, _, _ := strings.Cut(v, ",")
			if strings.TrimSpace(strings.ToLower(scheme)) == "https" {
				return true
			}
		}
	}
	if v := header.Get("Forwarded"); v != "" {
		for _, element := range strings.Split(v, ",") {
			for _, param := range strings.Split(element, ";") {
				param = strings.TrimSpace(param)
				if k, val, ok := strings.Cut(param, "="); ok && strings.ToLower(strings.TrimSpace(k)) == "proto" {
					if strings.ToLower(strings.Trim(strings.TrimSpace(val), "\"")) == "https" {
						return true
					}
				}
			}
		}
	}
	if strings.EqualFold(header.Get("Front-End-Https"), "on") || strings.EqualFold(header.Get("X-Forwarded-Ssl"), "on") {
		return true
	}
	return false
}

func ParseContentLength(contentLength string) int64 {
	if contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			return size
		}
	}
	return -1
}

func TransformHeaders(header http.Header) [][2]string {
	headers := make([][2]string, 0)
	for k, values := range header {
		for _, v := range values {
			headers = append(headers, [2]string{k, v})
		}
	}
	return headers
}
