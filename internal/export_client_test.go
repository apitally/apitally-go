package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestExportRequestsCarryAuthorizationEnvironmentAndEncodingHeaders(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.Env = "prod"
	startRuntimeForTest(t, server, cfg)

	require.NoError(t, Shutdown(context.Background()))

	requests := server.Requests()
	require.NotEmpty(t, requests)
	for _, req := range requests {
		assert.Equal(t, "Bearer "+testutils.WriteToken, req.Header.Get("Authorization"))
		assert.Equal(t, "prod", req.Header.Get("Apitally-Env"))
		assert.Equal(t, "application/x-protobuf", req.Header.Get("Content-Type"))
		assert.Equal(t, "gzip", req.Header.Get("Content-Encoding"))
		assert.True(t, strings.HasPrefix(req.Header.Get("User-Agent"), "apitally-go/"))
	}
}
