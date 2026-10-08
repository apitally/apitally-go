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

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	assert.Equal(t, "/items/\uFFFD", testutils.Attributes(spans[0].Attributes)["url.path"])
}
