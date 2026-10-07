package internal

import (
	"encoding/json"
	"runtime"

	"go.opentelemetry.io/otel/attribute"
)

const startupEventName = "apitally.app.startup"

// FrameworkInfo describes the framework integration calling Register.
type FrameworkInfo struct {
	// Name is the framework name reported at startup, such as "gin".
	Name string
	// ModulePath is the framework's module path, used to look up its version.
	ModulePath string
	// ScopeName is the Apitally framework module path, used as the
	// instrumentation scope of SERVER spans.
	ScopeName string
}

// Route is a registered method and route template.
type Route struct {
	Method string
	Path   string
}

// startupEventBody returns the startup event payload as a JSON string. It
// omits the write token, endpoint, disabled flag, environment and app
// version, and represents configured callbacks as true.
func startupEventBody(s *settings, framework FrameworkInfo, routes []Route) attribute.Value {
	c := s.config
	versions := map[string]string{"go": runtime.Version(), framework.Name: moduleVersion(framework.ModulePath)}
	if c.AppVersion != "" {
		versions["app"] = c.AppVersion
	}
	config := map[string]any{
		"CaptureLogs":            c.CaptureLogs,
		"CaptureRequestHeaders":  c.CaptureRequestHeaders,
		"CaptureRequestBody":     c.CaptureRequestBody,
		"CaptureResponseHeaders": c.CaptureResponseHeaders,
		"CaptureResponseBody":    c.CaptureResponseBody,
		"SampleRate":             c.SampleRate,
		"MaskQueryParams":        append([]string{}, c.MaskQueryParams...),
		"MaskHeaders":            append([]string{}, c.MaskHeaders...),
		"MaskBodyFields":         append([]string{}, c.MaskBodyFields...),
		"ExcludePaths":           append([]string{}, c.ExcludePaths...),
	}
	for name, isSet := range map[string]bool{
		"SampleOnRequest":  c.SampleOnRequest != nil,
		"SampleOnResponse": c.SampleOnResponse != nil,
		"MaskRequestBody":  c.MaskRequestBody != nil,
		"MaskResponseBody": c.MaskResponseBody != nil,
		"MaskLogRecord":    c.MaskLogRecord != nil,
	} {
		if isSet {
			config[name] = true
		}
	}
	type path struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	paths := []path{}
	seen := map[Route]bool{}
	for _, route := range routes {
		if route.Method != "HEAD" && route.Method != "OPTIONS" && !seen[route] {
			seen[route] = true
			paths = append(paths, path(route))
		}
	}
	body, _ := json.Marshal(map[string]any{
		"framework": framework.Name,
		"versions":  versions,
		"config":    config,
		"paths":     paths,
	})
	return attribute.StringValue(string(body))
}
