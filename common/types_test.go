package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfig(t *testing.T) {
	config := NewConfig("test-client-id")

	assert.Equal(t, "test-client-id", config.ClientID)
	assert.Equal(t, "dev", config.Env)

	assert.NotNil(t, config.RequestLogging)
	assert.False(t, config.RequestLogging.Enabled)
	assert.True(t, config.RequestLogging.LogQueryParams)
	assert.False(t, config.RequestLogging.LogRequestHeaders)
	assert.False(t, config.RequestLogging.LogRequestBody)
	assert.True(t, config.RequestLogging.LogResponseHeaders)
	assert.False(t, config.RequestLogging.LogResponseBody)
	assert.True(t, config.RequestLogging.LogPanic)
}
