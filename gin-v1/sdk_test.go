package apitally_test

import (
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitally "github.com/apitally/apitally-go/gin-v1"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCaptureErrorRecordsCallerStack(t *testing.T) {
	server := setUp(t)
	r := gin.New()
	apitally.Init(r, nil)
	r.GET("/items", func(c *gin.Context) {
		apitally.CaptureError(c, errors.New("failed"))
	})
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	assert.Regexp(t, `^github.com/apitally/apitally-go/gin-v1_test.TestCaptureErrorRecordsCallerStack.func1\n\t\S+/gin-v1/sdk_test.go:\d+\n`, testutils.Attributes(spans[0].Events[0].Attributes)["exception.stacktrace"])
}
