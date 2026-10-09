package internal

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

func TestInvalidUTF8IsReplacedInExportedTelemetry(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(writeOK))

	testutils.Get(t, appURL+"/items/%ff")
	require.NoError(t, Shutdown(context.Background()))

	span := server.SingleSpan(t)
	assert.Equal(t, "/items/\uFFFD", testutils.Attributes(span.Attributes)["url.path"])
}
