package apitally_test

import (
	"errors"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitally "github.com/apitally/apitally-go/echo-v5"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCaptureErrorRecordsCallerStack(t *testing.T) {
	server := setUp(t)
	e := echo.New()
	apitally.Init(e, nil)
	e.GET("/items", func(c *echo.Context) error {
		apitally.CaptureError(c.Request().Context(), errors.New("failed"))
		return nil
	})
	appURL := testutils.Serve(t, e)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	assert.Regexp(t, `^github.com/apitally/apitally-go/echo-v5_test.TestCaptureErrorRecordsCallerStack.func1\n\t\S+/echo-v5/sdk_test.go:\d+\n`, testutils.Attributes(spans[0].Events[0].Attributes)["exception.stacktrace"])
}
