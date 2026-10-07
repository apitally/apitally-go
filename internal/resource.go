package internal

import (
	"crypto/rand"
	"fmt"
	"runtime/debug"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	rootModulePath = "github.com/apitally/apitally-go"
	distroName     = "apitally-go"
)

var (
	// The server treats a restarted process as a new instance.
	instanceID = newUUID()
	sdkVersion = moduleVersion(rootModulePath)
)

// newResource builds the resource from OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES, with the Apitally-owned keys merged on top, so the
// environment always matches the Apitally-Env export header.
func newResource(env string) *resource.Resource {
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.instance.id", instanceID),
		attribute.String("deployment.environment.name", env),
		attribute.String("telemetry.distro.name", distroName),
		attribute.String("telemetry.distro.version", sdkVersion),
	))
	if err != nil {
		logDebug("Apitally resource merge reported an error", "error", err)
	}
	return res
}

// moduleVersion returns the version of a module linked into the binary, or
// "unknown" when the build embeds no module information.
func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Path == path {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			return dep.Version
		}
	}
	return "unknown"
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
