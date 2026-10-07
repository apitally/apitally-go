// Package testutils holds test helpers shared by the root and framework
// modules. It never imports the internal package, whose tests import it.
package testutils

import "testing"

// ClearEnv clears the environment variables Apitally reads for the duration
// of the test.
func ClearEnv(t testing.TB) {
	for _, name := range []string{"APITALLY_WRITE_TOKEN", "APITALLY_ENV", "APITALLY_DISABLED", "APITALLY_OTLP_ENDPOINT", "OTEL_SDK_DISABLED"} {
		t.Setenv(name, "")
	}
}
